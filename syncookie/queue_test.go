package syncookie

import "testing"

// TestAcceptFullBothPaths fills the accept queue and checks that both ACK
// paths report ErrAcceptFull while retaining the half-open entry, plus FIFO
// ordering and ErrEmpty.
func TestAcceptFullBothPaths(t *testing.T) {
	const A = 2
	v, _ := New(1, 1_000_000, A, testHash, newCounterISN())
	k1, k2, k3, k4 := mkKey(1), mkKey(2), mkKey(3), mkKey(4)

	// k1 takes the half-open slot (sisn 1000).
	if _, err := v.OnSyn(0, k1, 11, 1460); err != nil {
		t.Fatal(err)
	}
	// Half-open full: k2, k3 fall back to cookies and establish.
	r2, err := v.OnSyn(1, k2, 21, 1460)
	if err != nil || !r2.Cookie {
		t.Fatalf("k2 syn: %+v %v", r2, err)
	}
	r3, err := v.OnSyn(2, k3, 31, 536)
	if err != nil || !r3.Cookie {
		t.Fatalf("k3 syn: %+v %v", r3, err)
	}
	if mss, err := v.OnAck(3, k2, 22, r2.ISN+1); err != nil || mss != 1460 {
		t.Fatalf("k2 ack: mss=%d %v", mss, err)
	}
	if mss, err := v.OnAck(4, k3, 32, r3.ISN+1); err != nil || mss != 536 {
		t.Fatalf("k3 ack: mss=%d %v", mss, err)
	}
	if s := v.Stats(); s.Acceptable != A {
		t.Fatalf("accept queue want %d, got %+v", A, s)
	}

	// Half-open path: matching k1 ACK but accept full; entry retained.
	if _, err := v.OnAck(5, k1, 12, 1001); err != ErrAcceptFull {
		t.Fatalf("half-open path want ErrAcceptFull, got %v", err)
	}
	if _, ok := v.half[k1]; !ok {
		t.Fatal("half-open entry must be retained on ErrAcceptFull")
	}

	// SYN while accept full is dropped before slot/cookie logic.
	if _, err := v.OnSyn(6, mkKey(9), 91, 1460); err != ErrAcceptFull {
		t.Fatalf("syn want ErrAcceptFull, got %v", err)
	}

	// Cookie path: well-formed age-0 cookie at tick 0 also stops at accept-full.
	h := testHash(k4.CAddr, k4.SAddr, k4.CPort, k4.SPort, 41, 0) & 0xFFFFFF
	if _, err := v.OnAck(7, k4, 42, h+1); err != ErrAcceptFull {
		t.Fatalf("cookie path want ErrAcceptFull, got %v", err)
	}
	if s := v.Stats(); s.CookieOK != 2 {
		t.Fatalf("rejected cookie must not count: %+v", s)
	}

	// Drain one slot; the retained k1 ACK now succeeds.
	if gotKey, mss, err := v.Accept(); err != nil || gotKey != k2 || mss != 1460 {
		t.Fatalf("Accept want k2/1460, got %v %d %v", gotKey, mss, err)
	}
	if _, err := v.OnAck(8, k1, 12, 1001); err != nil {
		t.Fatalf("k1 ack after drain: %v", err)
	}
	if _, ok := v.half[k1]; ok {
		t.Fatal("k1 should have left half-open")
	}
	// FIFO order: k3 then k1.
	if key, mss, _ := v.Accept(); key != k3 || mss != 536 {
		t.Fatalf("want k3/536, got %v/%d", key, mss)
	}
	if key, mss, _ := v.Accept(); key != k1 || mss != 1460 {
		t.Fatalf("want k1/1460, got %v/%d", key, mss)
	}
	if _, _, err := v.Accept(); err != ErrEmpty {
		t.Fatalf("want ErrEmpty, got %v", err)
	}
}

// TestBadAckRetainsEntry checks sequence mismatches on the half-open path.
func TestBadAckRetainsEntry(t *testing.T) {
	v, _ := New(2, 1_000_000, 4, testHash, newCounterISN())
	k1 := mkKey(1)
	if _, err := v.OnSyn(100, k1, 1000, 1460); err != nil {
		t.Fatal(err)
	}
	// Wrong ack (not sisn+1=1001).
	if _, err := v.OnAck(101, k1, 1001, 999); err != ErrBadAck {
		t.Fatalf("want ErrBadAck, got %v", err)
	}
	// Wrong seq (not cisn+1=1001).
	if _, err := v.OnAck(102, k1, 1002, 1001); err != ErrBadAck {
		t.Fatalf("want ErrBadAck, got %v", err)
	}
	// 32-bit wraparound: cisn=0xFFFFFFFF -> expected seq 0.
	k2 := mkKey(2)
	r, err := v.OnSyn(103, k2, 0xFFFFFFFF, 1460)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.OnAck(104, k2, 0, r.ISN+1); err != nil {
		t.Fatalf("wraparound seq: %v", err)
	}
	if s := v.Stats(); s.HalfOpen != 1 || s.Acceptable != 1 {
		t.Fatalf("stats: %+v", s)
	}
}

// TestRejectedOpSkipsCleanup proves a rejected op never prunes: an expired
// entry survives a rejected op and is removed only by a later accepted op.
func TestRejectedOpSkipsCleanup(t *testing.T) {
	// T=100: an entry created at 0 expires exactly at now=100.
	v3, _ := New(1, 100, 2, testHash, newCounterISN())
	kx, ky := mkKey(61), mkKey(62)
	if _, err := v3.OnSyn(0, kx, 11, 1460); err != nil {
		t.Fatal(err)
	}
	// Fill the accept queue through cookie connections.
	for i, key := range []Key{mkKey(71), mkKey(72)} {
		rr, err := v3.OnSyn(2+int64(i), key, 300, 1460)
		if err != nil || !rr.Cookie {
			t.Fatalf("fill syn %d: %+v %v", i, rr, err)
		}
		if _, err := v3.OnAck(3+int64(i), key, 301, rr.ISN+1); err != nil {
			t.Fatalf("fill ack %d: %v", i, err)
		}
	}
	// kx (created 0) is expired at now>=100. Rejected (input-invalid) ops
	// must not prune it.
	// Invalid-time op also rejects without cleanup.
	if _, err := v3.OnSyn(-1, ky, 22, 1460); err != ErrInvalidTime {
		t.Fatalf("want ErrInvalidTime, got %v", err)
	}
	if _, ok := v3.half[kx]; !ok {
		t.Fatal("expired entry must survive an invalid-time op")
	}
	// Clock rollback op rejects without cleanup.
	if _, err := v3.OnSyn(3, ky, 22, 1460); err != ErrClockBack {
		t.Fatalf("want ErrClockBack, got %v", err)
	}
	if _, ok := v3.half[kx]; !ok {
		t.Fatal("expired entry must survive a rollback op")
	}
	// Out-of-range mss is rejected before cleanup as well.
	if _, err := v3.OnSyn(100, ky, 22, 70000); err != ErrInvalidMSS {
		t.Fatalf("want ErrInvalidMSS, got %v", err)
	}
	if _, ok := v3.half[kx]; !ok {
		t.Fatal("expired entry must survive an invalid-mss op")
	}
	// An accepted op at 100 prunes kx even though the SYN itself is dropped
	// due to the full accept queue (accepted ops always clean up first).
	if _, err := v3.OnSyn(100, ky, 22, 1460); err != ErrAcceptFull {
		t.Fatalf("want ErrAcceptFull, got %v", err)
	}
	if _, ok := v3.half[kx]; ok {
		t.Fatal("expired entry must be pruned by the accepted op")
	}
	// Drain one slot; a fresh SYN at 100 registers ky.
	if _, _, err := v3.Accept(); err != nil {
		t.Fatal(err)
	}
	if _, err := v3.OnSyn(100, ky, 22, 1460); err != nil {
		t.Fatalf("accepted syn after drain: %v", err)
	}
	if _, ok := v3.half[kx]; ok {
		t.Fatal("expired entry must be pruned by the accepted op")
	}
	if _, ok := v3.half[ky]; !ok {
		t.Fatal("ky must now be registered")
	}
}

// TestInvalidArgsAndRollback covers parameter validation precedence.
func TestInvalidArgsAndRollback(t *testing.T) {
	if _, err := New(0, 10, 4, testHash, newCounterISN()); err != ErrInvalidParam {
		t.Fatalf("B=0 want ErrInvalidParam, got %v", err)
	}
	if _, err := New(1025, 10, 4, testHash, newCounterISN()); err != ErrInvalidParam {
		t.Fatalf("B=1025 want ErrInvalidParam, got %v", err)
	}
	if _, err := New(1, 0, 4, testHash, newCounterISN()); err != ErrInvalidParam {
		t.Fatalf("T=0 want ErrInvalidParam, got %v", err)
	}
	if _, err := New(1, 1_000_001, 4, testHash, newCounterISN()); err != ErrInvalidParam {
		t.Fatalf("T big want ErrInvalidParam, got %v", err)
	}
	if _, err := New(1, 10, 0, testHash, newCounterISN()); err != ErrInvalidParam {
		t.Fatalf("A=0 want ErrInvalidParam, got %v", err)
	}
	if _, err := New(1, 10, 4, nil, newCounterISN()); err != ErrInvalidParam {
		t.Fatalf("nil hash want ErrInvalidParam, got %v", err)
	}
	if _, err := New(1, 10, 4, testHash, nil); err != ErrInvalidParam {
		t.Fatalf("nil isn want ErrInvalidParam, got %v", err)
	}

	v, _ := New(1, 10, 4, testHash, newCounterISN())
	// Time errors precede mss errors.
	if _, err := v.OnSyn(-1, mkKey(1), 1, 99999); err != ErrInvalidTime {
		t.Fatalf("want ErrInvalidTime, got %v", err)
	}
	if _, err := v.OnSyn(maxNow+1, mkKey(1), 1, 99999); err != ErrInvalidTime {
		t.Fatalf("want ErrInvalidTime, got %v", err)
	}
	if _, err := v.OnSyn(10, mkKey(1), 1, 65536); err != ErrInvalidMSS {
		t.Fatalf("want ErrInvalidMSS, got %v", err)
	}
	if _, err := v.OnAck(-1, mkKey(1), 1, 1); err != ErrInvalidTime {
		t.Fatalf("ack want ErrInvalidTime, got %v", err)
	}
	// Accepted op establishes the clock.
	if _, err := v.OnSyn(100, mkKey(1), 1, 1460); err != nil {
		t.Fatal(err)
	}
	// Rollback on either op; even with bad mss, time wins.
	if _, err := v.OnSyn(99, mkKey(1), 1, 99999); err != ErrClockBack {
		t.Fatalf("want ErrClockBack, got %v", err)
	}
	if _, err := v.OnAck(99, mkKey(1), 1, 1); err != ErrClockBack {
		t.Fatalf("ack want ErrClockBack, got %v", err)
	}
	// Equal time is allowed (not a rollback).
	if _, err := v.OnSyn(100, mkKey(1), 1, 1460); err != nil {
		t.Fatalf("equal now retrans path err: %v", err)
	}
}
