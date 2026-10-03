package syncookie

import "testing"

// TestWorkedExample reproduces the reference scenario: B=1, T=5000,
// k1 enqueued at now=130000 (t=2), k2 overflowed to a cookie at
// now=130001, k1 retransmitted at 134999 and expired at 135000.
func TestWorkedExample(t *testing.T) {
	v, err := New(1, 5000, 4, testHash, isnGen(1000))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// k1 SYN at now=130000 (t=2): registered as half-open.
	r1, err := v.OnSyn(130000, k1(), 100, 1400)
	if err != nil {
		t.Fatalf("k1 syn: %v", err)
	}
	if r1.Cookie || r1.ISN != 1000 {
		t.Fatalf("k1 syn = %+v, want {ISN:1000 Cookie:false}", r1)
	}
	if s := v.Stats(); s.HalfOpen != 1 {
		t.Fatalf("half-open = %d, want 1", s.HalfOpen)
	}

	// k2 SYN at now=130001: half-open full, stateless cookie.
	r2, err := v.OnSyn(130001, k2(), 200, 1400)
	if err != nil {
		t.Fatalf("k2 syn: %v", err)
	}
	if !r2.Cookie {
		t.Fatalf("k2 syn should be a cookie, got %+v", r2)
	}
	// t=2, mi=1 (1220 <= 1400 < 1460): high byte (2<<27|1<<24)>>24 = 0x11.
	if hi := r2.ISN & 0xFF000000; hi != 0x11000000 {
		t.Fatalf("cookie high bits = %#08x, want 0x11000000", hi)
	}
	wantH := testHash(k2().CAddr, k2().SAddr, k2().CPort, k2().SPort, 200, 2) & 0xFFFFFF
	if lo := r2.ISN & 0xFFFFFF; lo != wantH {
		t.Fatalf("cookie hash bits = %#06x, want %#06x", lo, wantH)
	}
	if s := v.Stats(); s.CookieSent != 1 || s.HalfOpen != 1 {
		t.Fatalf("stats = %+v, want CookieSent=1 HalfOpen=1", s)
	}

	// mi mapping: 500->0, 1459->1, 1460->2, 8960->3 (queue still full).
	for i, tc := range []struct {
		mss  int
		want uint32
	}{{500, 0}, {1459, 1}, {1460, 2}, {8960, 3}} {
		key := Key{CAddr: 0x0A000010 + uint32(i), CPort: 50000, SAddr: 0x0A0000FE, SPort: 80}
		r, err := v.OnSyn(130001, key, 300, tc.mss)
		if err != nil {
			t.Fatalf("mss=%d syn: %v", tc.mss, err)
		}
		if !r.Cookie {
			t.Fatalf("mss=%d should be a cookie", tc.mss)
		}
		if mi := (r.ISN >> 24) & 7; mi != tc.want {
			t.Fatalf("mss=%d mi=%d, want %d", tc.mss, mi, tc.want)
		}
	}

	// k1 retransmitted at 134999: same sisn, retrans counted, created kept.
	r3, err := v.OnSyn(134999, k1(), 100, 1400)
	if err != nil {
		t.Fatalf("k1 retrans: %v", err)
	}
	if r3.Cookie || r3.ISN != 1000 {
		t.Fatalf("k1 retrans = %+v, want {ISN:1000 Cookie:false}", r3)
	}
	if s := v.Stats(); s.Retrans != 1 {
		t.Fatalf("retrans = %d, want 1", s.Retrans)
	}

	// At 135000 the k1 entry (created 130000, T=5000) is exactly expired;
	// retransmission did not refresh created, so this SYN is a new connection.
	r4, err := v.OnSyn(135000, k1(), 100, 1400)
	if err != nil {
		t.Fatalf("k1 re-syn: %v", err)
	}
	if r4.Cookie || r4.ISN != 1007 {
		t.Fatalf("k1 re-syn = %+v, want {ISN:1007 Cookie:false}", r4)
	}
	if s := v.Stats(); s.Retrans != 1 || s.HalfOpen != 1 {
		t.Fatalf("stats = %+v, want Retrans=1 HalfOpen=1", s)
	}
}
