package syncookie

import (
	"errors"
	"testing"
)

// TestAcceptFullBothPaths: with the pending queue full, both the half-open
// ACK path and the cookie ACK path fail with ErrAcceptFull; the half-open
// entry is retained and can complete after an Accept.
func TestAcceptFullBothPaths(t *testing.T) {
	v := newFilled(t, 2, 1_000_000, 1)
	r1, err := v.OnSyn(1000, k1(), 100, 1400)
	if err != nil {
		t.Fatalf("k1 syn: %v", err)
	}
	r2, err := v.OnSyn(1000, k2(), 200, 1400)
	if err != nil {
		t.Fatalf("k2 syn: %v", err)
	}
	c3, err := v.OnSyn(1000, k3(), 300, 1400) // half-open full -> cookie
	if err != nil {
		t.Fatalf("k3 syn: %v", err)
	}
	if !c3.Cookie {
		t.Fatalf("k3 should be a cookie, got %+v", c3)
	}

	// Establish k2: fills the only pending slot.
	if err := v.OnAck(1001, k2(), 201, r2.ISN+1); err != nil {
		t.Fatalf("k2 ack: %v", err)
	}

	// Half-open path: pending full -> ErrAcceptFull, k1 entry retained.
	if err := v.OnAck(1002, k1(), 101, r1.ISN+1); !errors.Is(err, ErrAcceptFull) {
		t.Fatalf("k1 ack = %v, want ErrAcceptFull", err)
	}
	if s := v.Stats(); s.HalfOpen != 1 {
		t.Fatalf("half-open = %d, want 1 (k1 retained)", s.HalfOpen)
	}
	// Cookie path: pending full -> ErrAcceptFull (after cookie checks pass).
	if err := v.OnAck(1002, k3(), 301, c3.ISN+1); !errors.Is(err, ErrAcceptFull) {
		t.Fatalf("k3 ack = %v, want ErrAcceptFull", err)
	}
	if s := v.Stats(); s.CookieOK != 0 {
		t.Fatalf("cookieOK = %d, want 0", s.CookieOK)
	}

	// SYN for a new key is also dropped while pending is full.
	if _, err := v.OnSyn(1003, k4(), 400, 1400); !errors.Is(err, ErrAcceptFull) {
		t.Fatalf("k4 syn = %v, want ErrAcceptFull", err)
	}

	// Drain one slot; both retained connections now complete, FIFO order.
	key, _, err := v.Accept()
	if err != nil || key != k2() {
		t.Fatalf("accept = (%+v, %v), want (k2, nil)", key, err)
	}
	if err := v.OnAck(1004, k1(), 101, r1.ISN+1); err != nil {
		t.Fatalf("k1 retry ack: %v", err)
	}
	key, mss, err := v.Accept()
	if err != nil || key != k1() || mss != 1400 {
		t.Fatalf("accept = (%+v, %d, %v), want (k1, 1400, nil)", key, mss, err)
	}
	if err := v.OnAck(1005, k3(), 301, c3.ISN+1); err != nil {
		t.Fatalf("k3 retry ack: %v", err)
	}
	if s := v.Stats(); s.CookieOK != 1 || s.Pending != 1 {
		t.Fatalf("stats = %+v, want CookieOK=1 Pending=1", s)
	}
}

// TestBadAck: wrong ack/seq against a half-open entry yields ErrBadAck and
// keeps the entry; the correct ACK still completes afterwards.
func TestBadAck(t *testing.T) {
	v := newFilled(t, 1, 1_000_000, 2)
	r, err := v.OnSyn(1000, k1(), 100, 1400)
	if err != nil {
		t.Fatalf("k1 syn: %v", err)
	}
	if err := v.OnAck(1001, k1(), 101, r.ISN+2); !errors.Is(err, ErrBadAck) {
		t.Fatalf("wrong ack = %v, want ErrBadAck", err)
	}
	if err := v.OnAck(1001, k1(), 102, r.ISN+1); !errors.Is(err, ErrBadAck) {
		t.Fatalf("wrong seq = %v, want ErrBadAck", err)
	}
	if s := v.Stats(); s.HalfOpen != 1 || s.Pending != 0 {
		t.Fatalf("stats = %+v, want HalfOpen=1 Pending=0", s)
	}
	if err := v.OnAck(1002, k1(), 101, r.ISN+1); err != nil {
		t.Fatalf("correct ack: %v", err)
	}
}

// TestRejectedOpsDoNotClean: rejected operations (invalid params, clock
// regression) must not clean expired entries, move the clock, or change
// any counter; the expired entry is removed by the next accepted operation.
func TestRejectedOpsDoNotClean(t *testing.T) {
	v := newFilled(t, 1, 5000, 4)
	if _, err := v.OnSyn(130000, k1(), 100, 1400); err != nil {
		t.Fatalf("k1 syn: %v", err)
	}

	// k1 expires at 135000. A rejected op at 136000 must not clean it.
	if _, err := v.OnSyn(136000, k2(), 200, -1); !errors.Is(err, ErrInvalidMSS) {
		t.Fatalf("mss=-1 = %v, want ErrInvalidMSS", err)
	}
	if _, err := v.OnSyn(136000, k2(), 200, 65536); !errors.Is(err, ErrInvalidMSS) {
		t.Fatalf("mss=65536 = %v, want ErrInvalidMSS", err)
	}
	if s := v.Stats(); s.HalfOpen != 1 {
		t.Fatalf("half-open = %d after rejected ops, want 1 (no cleanup)", s.HalfOpen)
	}

	// The clock did not move: 130000 is still acceptable (not backwards).
	r, err := v.OnSyn(130000, k1(), 100, 1400)
	if err != nil {
		t.Fatalf("k1 retrans at 130000: %v", err)
	}
	if r.Cookie || r.ISN != 1000 {
		t.Fatalf("k1 retrans = %+v, want {ISN:1000 Cookie:false}", r)
	}

	// Invalid now values are rejected before anything else.
	if _, err := v.OnSyn(-1, k2(), 200, 1400); !errors.Is(err, ErrInvalidNow) {
		t.Fatalf("now=-1 = %v, want ErrInvalidNow", err)
	}
	if _, err := v.OnSyn(1_000_000_000_001, k2(), 200, 1400); !errors.Is(err, ErrInvalidNow) {
		t.Fatalf("now=1e12+1 = %v, want ErrInvalidNow", err)
	}
	if err := v.OnAck(-5, k2(), 201, 1); !errors.Is(err, ErrInvalidNow) {
		t.Fatalf("ack now=-5 = %v, want ErrInvalidNow", err)
	}

	// The next accepted op at 136000 cleans the expired k1 entry.
	if _, err := v.OnSyn(136000, k2(), 200, 1400); err != nil {
		t.Fatalf("k2 syn at 136000: %v", err)
	}
	if s := v.Stats(); s.HalfOpen != 1 {
		t.Fatalf("half-open = %d, want 1 (k2 only)", s.HalfOpen)
	}

	// Clock regression is distinguishable and rejected without state change.
	before := v.Stats()
	if _, err := v.OnSyn(135999, k4(), 400, 1400); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("backwards syn = %v, want ErrClockBackwards", err)
	}
	if err := v.OnAck(100, k4(), 401, 1); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("backwards ack = %v, want ErrClockBackwards", err)
	}
	if after := v.Stats(); after != before {
		t.Fatalf("stats changed by rejected ops: %+v -> %+v", before, after)
	}
}

// TestInvalidConstructorParams covers constructor range checks.
func TestInvalidConstructorParams(t *testing.T) {
	okHash := testHash
	okISN := isnGen(0)
	cases := []struct {
		name string
		b    int
		to   int64
		a    int
		h    HashFunc
		n    func() uint32
	}{
		{"B=0", 0, 5000, 1, okHash, okISN},
		{"B=1025", 1025, 5000, 1, okHash, okISN},
		{"T=0", 1, 0, 1, okHash, okISN},
		{"T=1e6+1", 1, 1_000_001, 1, okHash, okISN},
		{"A=0", 1, 5000, 0, okHash, okISN},
		{"A=1025", 1, 5000, 1025, okHash, okISN},
		{"nil hash", 1, 5000, 1, nil, okISN},
		{"nil nextISN", 1, 5000, 1, okHash, nil},
	}
	for _, tc := range cases {
		if _, err := New(tc.b, tc.to, tc.a, tc.h, tc.n); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("%s: err = %v, want ErrInvalidParam", tc.name, err)
		}
	}
	// Boundary values are accepted.
	if _, err := New(1, 1, 1, okHash, okISN); err != nil {
		t.Fatalf("min params: %v", err)
	}
	if _, err := New(1024, 1_000_000, 1024, okHash, okISN); err != nil {
		t.Fatalf("max params: %v", err)
	}
}

// TestAcceptEmpty: Accept on an empty pending queue yields ErrEmpty.
func TestAcceptEmpty(t *testing.T) {
	v := newFilled(t, 1, 5000, 1)
	if _, _, err := v.Accept(); !errors.Is(err, ErrEmpty) {
		t.Fatalf("accept = %v, want ErrEmpty", err)
	}
}

// TestHalfOpenExpiryBoundary: created+T == now is expired (exactly equal).
func TestHalfOpenExpiryBoundary(t *testing.T) {
	v := newFilled(t, 1, 5000, 4)
	if _, err := v.OnSyn(10000, k1(), 100, 1400); err != nil {
		t.Fatalf("k1 syn: %v", err)
	}
	// 14999 < 10000+5000: still alive -> retransmission.
	r, err := v.OnSyn(14999, k1(), 100, 1400)
	if err != nil || r.Cookie || r.ISN != 1000 {
		t.Fatalf("at 14999 = (%+v, %v), want retrans of ISN 1000", r, err)
	}
	// 15000 == 10000+5000: expired -> new entry with a fresh ISN.
	r, err = v.OnSyn(15000, k1(), 100, 1400)
	if err != nil || r.Cookie || r.ISN != 1007 {
		t.Fatalf("at 15000 = (%+v, %v), want new entry ISN 1007", r, err)
	}
}
