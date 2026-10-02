package syncookie

import "testing"

// TestRequiredScenario walks every example called out in the specification.
func TestRequiredScenario(t *testing.T) {
	// B=1, T=5000ms, A=4.
	v, err := New(1, 5000, 4, testHash, newCounterISN())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	k1, k2 := mkKey(1), mkKey(2)

	// now=130000 -> tick floor(130000/64000)=2. k1 occupies the half-open slot.
	r, err := v.OnSyn(130000, k1, 101, 1400)
	if err != nil || r.Cookie || r.ISN != 1000 {
		t.Fatalf("k1 SYN: %+v err=%v", r, err)
	}
	if got := v.Stats(); got.HalfOpen != 1 {
		t.Fatalf("half-open want 1, got %+v", got)
	}

	// now=130001: half-open full -> k2 gets a cookie; mss=1400 -> mi=1 (1220).
	r2, err := v.OnSyn(130001, k2, 201, 1400)
	if err != nil || !r2.Cookie {
		t.Fatalf("k2 SYN: %+v err=%v", r2, err)
	}
	if hi := r2.ISN & 0xFF000000; hi != 0x11000000 {
		t.Fatalf("cookie high bits want 0x11000000, got %#010x", hi)
	}
	wantLow := testHash(k2.CAddr, k2.SAddr, k2.CPort, k2.SPort, 201, 2) & 0xFFFFFF
	if r2.ISN&0xFFFFFF != wantLow {
		t.Fatalf("cookie low bits want %#x, got %#x", wantLow, r2.ISN&0xFFFFFF)
	}
	if s := v.Stats(); s.CookieSent != 1 {
		t.Fatalf("cookieSent want 1, got %d", s.CookieSent)
	}

	// MSS table floor selection: 500/1459/1460/8960 -> mi 0/1/2/3.
	mssCases := []struct {
		mss uint32
		mi  uint32
	}{
		{500, 0}, {1459, 1}, {1460, 2}, {8960, 3},
	}
	for i, c := range mssCases {
		key := mkKey(10 + uint32(i))
		rr, err := v.OnSyn(130002+int64(i), key, 300, c.mss)
		if err != nil || !rr.Cookie {
			t.Fatalf("mss %d: %+v err=%v", c.mss, rr, err)
		}
		if mi := (rr.ISN >> 24) & 7; mi != c.mi {
			t.Fatalf("mss %d: mi want %d got %d (isn %#010x)", c.mss, c.mi, mi, rr.ISN)
		}
	}

	// Exhaustive floor boundaries.
	for _, c := range []struct {
		mss uint32
		mi  uint32
	}{
		{0, 0}, {535, 0}, {536, 0}, {1219, 0}, {1220, 1},
		{1459, 1}, {1460, 2}, {8959, 2}, {8960, 3},
		{9000, 3}, {65535, 3},
	} {
		if got := mssIndex(c.mss); got != c.mi {
			t.Fatalf("mssIndex(%d)=%d want %d", c.mss, got, c.mi)
		}
	}

	// k1 retransmits at 134999 (created+T=135000, still alive): same sisn,
	// created untouched, retrans +1.
	rr, err := v.OnSyn(134999, k1, 101, 1400)
	if err != nil || rr.Cookie || rr.ISN != 1000 {
		t.Fatalf("k1 retrans: %+v err=%v", rr, err)
	}
	if v.half[k1].created != 130000 {
		t.Fatalf("retrans changed created: %d", v.half[k1].created)
	}
	if s := v.Stats(); s.Retrans != 1 {
		t.Fatalf("retrans want 1, got %d", s.Retrans)
	}

	// 135000: created+T <= now -> expired; SYN becomes a brand-new connection
	// and gets the next server ISN (1001).
	rr, err = v.OnSyn(135000, k1, 101, 1400)
	if err != nil || rr.Cookie || rr.ISN != 1001 {
		t.Fatalf("k1 after expiry: %+v err=%v", rr, err)
	}
	if v.half[k1].created != 135000 {
		t.Fatalf("new created want 135000, got %d", v.half[k1].created)
	}
	if s := v.Stats(); s.Retrans != 1 || s.HalfOpen != 1 {
		t.Fatalf("stats after expiry-recreate: %+v", s)
	}

	// k2 cookie ACK at now=192000 (tick 3, age 1): valid, negotiated mss 1220.
	mss, err := v.OnAck(192000, k2, 202, r2.ISN+1)
	if err != nil || mss != 1220 {
		t.Fatalf("k2 cookie ACK age=1: mss=%d err=%v", mss, err)
	}
	if s := v.Stats(); s.CookieOK != 1 || s.Acceptable != 1 {
		t.Fatalf("stats after cookie OK: %+v", s)
	}

	// Same cookie at now=256000 (tick 4, age 2): ErrCookieExpired.
	if _, err = v.OnAck(256000, k2, 202, r2.ISN+1); err != ErrCookieExpired {
		t.Fatalf("want ErrCookieExpired, got %v", err)
	}
	if s := v.Stats(); s.CookieOK != 1 {
		t.Fatalf("failed cookie must not count: %+v", s)
	}

	// t5 wrap-around: at tick 32, cookies stamped 31 (age 1) and 32 (age 0)
	// validate; stamped 30 (age 2) is expired.
	vw, _ := New(1, 5000, 4, testHash, newCounterISN())
	cookieAt := func(key Key, cisn, tick uint32) uint32 {
		h := testHash(key.CAddr, key.SAddr, key.CPort, key.SPort, cisn, tick) & 0xFFFFFF
		return (tick%32)<<27 | h
	}
	now32 := int64(32) * msPerTick
	kw1, kw2, kw3 := mkKey(21), mkKey(22), mkKey(23)
	if _, err := vw.OnAck(now32, kw1, 502, cookieAt(kw1, 501, 31)+1); err != nil {
		t.Fatalf("wrap age=1: %v", err)
	}
	if _, err := vw.OnAck(now32, kw2, 602, cookieAt(kw2, 601, 32)+1); err != nil {
		t.Fatalf("wrap age=0: %v", err)
	}
	if _, err := vw.OnAck(now32, kw3, 702, cookieAt(kw3, 701, 30)+1); err != ErrCookieExpired {
		t.Fatalf("wrap age=2 want ErrCookieExpired, got %v", err)
	}

	// mi >= 4 -> ErrCookie (checked first on the cookie path).
	kb := mkKey(31)
	if _, err := vw.OnAck(now32+1, kb, 1, (uint32(4)<<24)+1); err != ErrCookie {
		t.Fatalf("mi=4 want ErrCookie, got %v", err)
	}
	// Hash mismatch -> ErrCookie.
	kh := mkKey(32)
	var stamp32 uint32 = 32
	badHash := stamp32<<27 | 12345 // t5 wraps to 0 at tick 32; mi 0; bogus hash
	if _, err := vw.OnAck(now32+2, kh, 1, badHash+1); err != ErrCookie {
		t.Fatalf("bad hash want ErrCookie, got %v", err)
	}
}
