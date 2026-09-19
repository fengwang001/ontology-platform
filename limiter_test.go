package ontology

import (
	"errors"
	"testing"
	"time"
)

func newTestLimiter(t *testing.T, cap, rate int64) *Limiter {
	t.Helper()
	l, err := NewLimiter(Config{Capacity: cap, Rate: rate, Shards: 7})
	if err != nil {
		t.Fatalf("NewLimiter: %v", err)
	}
	return l
}

// Refilling 100ms at rate 7/s must add 0.7 token, and that fraction must be
// retained until it becomes spendable. No float64 is used in the accounting.
func TestFractionalRefillAccumulates(t *testing.T) {
	l := newTestLimiter(t, 5, 7)
	t0 := time.Unix(1000, 0)

	// Drain the full bucket.
	if ok, err := l.Allow("a", 5, t0); !ok || err != nil {
		t.Fatalf("drain: ok=%v err=%v", ok, err)
	}
	// 100ms later: 0.7 token gained, still 0 whole tokens available.
	got, err := l.Available("a", t0.Add(100*time.Millisecond))
	if err != nil || got != 0 {
		t.Fatalf("after 100ms: available=%d err=%v, want 0", got, err)
	}
	// After 10 such intervals (1s): 7 tokens gained, capped at capacity 5.
	got, err = l.Available("a", t0.Add(1100*time.Millisecond))
	if err != nil || got != 5 {
		t.Fatalf("after refill: available=%d err=%v, want 5", got, err)
	}
}

// 1000 micro-advances of 1ms must produce exactly the same result as one
// single 1s advance: fractions must accumulate with zero drift. At
// 100 tokens/s one second adds exactly 10 tokens; the capacity is large
// enough that capping never interferes during the second.
func TestThousandMicroAdvancesEqualOneBigAdvance(t *testing.T) {
	const cap, rate = int64(100), int64(100)
	base := time.Unix(0, 0)

	micro := newTestLimiter(t, cap, rate)
	if _, err := micro.Allow("t", cap, base); err != nil {
		t.Fatal(err)
	}
	now := base
	for i := 0; i < 1000; i++ {
		now = now.Add(time.Millisecond)
		if _, err := micro.Available("t", now); err != nil {
			t.Fatal(err)
		}
	}
	microTokens := micro.shardFor("t").buckets["t"].tokens

	big := newTestLimiter(t, cap, rate)
	if _, err := big.Allow("t", cap, base); err != nil {
		t.Fatal(err)
	}
	if _, err := big.Available("t", base.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	bigTokens := big.shardFor("t").buckets["t"].tokens

	if microTokens != bigTokens {
		t.Fatalf("nanotokens differ: 1000x1ms=%d, 1x1s=%d", microTokens, bigTokens)
	}
	if microTokens != 100*scale {
		t.Fatalf("nanotokens=%d, want %d", microTokens, 100*scale)
	}
}

func TestRefillNeverExceedsCapacity(t *testing.T) {
	l := newTestLimiter(t, 3, 100)
	t0 := time.Unix(0, 0)
	if _, err := l.Allow("a", 3, t0); err != nil {
		t.Fatal(err)
	}
	got, err := l.Available("a", t0.Add(time.Hour))
	if err != nil || got != 3 {
		t.Fatalf("available=%d err=%v, want capped 3", got, err)
	}
}

func TestTimeReversalIsRejected(t *testing.T) {
	l := newTestLimiter(t, 5, 1)
	t0 := time.Unix(100, 0)
	if ok, _ := l.Allow("a", 2, t0); !ok {
		t.Fatal("first allow")
	}
	ok, err := l.Allow("a", 1, t0.Add(-time.Second))
	if ok || !errors.Is(err, ErrTimeReversed) {
		t.Fatalf("reversed: ok=%v err=%v", ok, err)
	}
	// No panic, no reset: state at t0 must be exactly as before (3 left).
	got, err := l.Available("a", t0)
	if err != nil || got != 3 {
		t.Fatalf("after reversal: available=%d err=%v, want 3", got, err)
	}
	// The rejected call never moved the clock: advancing from t0 still works.
	got, err = l.Available("a", t0.Add(time.Second))
	if err != nil || got != 4 {
		t.Fatalf("recovered: available=%d err=%v, want 4", got, err)
	}
}

// A read at the exact same timestamp must be stable; n==0 at the same
// timestamp must always pass and consume nothing.
func TestSameTimestampIsStable(t *testing.T) {
	l := newTestLimiter(t, 5, 1)
	now := time.Unix(7, 0)
	first, err := l.Available("a", now)
	if err != nil || first != 5 {
		t.Fatalf("first=%d err=%v", first, err)
	}
	for i := 0; i < 5; i++ {
		got, err := l.Available("a", now)
		if err != nil || got != first {
			t.Fatalf("repeat %d: %d err=%v, want %d", i, got, err, first)
		}
		ok, err := l.Allow("a", 0, now)
		if !ok || err != nil {
			t.Fatalf("n=0 repeat %d: ok=%v err=%v", i, ok, err)
		}
	}
}

func TestZeroRequestNeverConsumes(t *testing.T) {
	l := newTestLimiter(t, 2, 1)
	t0 := time.Unix(0, 0)
	for i := 0; i < 10; i++ {
		if ok, err := l.Allow("a", 0, t0); !ok || err != nil {
			t.Fatalf("zero request %d: ok=%v err=%v", i, ok, err)
		}
	}
	got, _ := l.Available("a", t0)
	if got != 2 {
		t.Fatalf("available after zero requests=%d, want 2", got)
	}
}

func TestNegativeAndOversizedRequests(t *testing.T) {
	l := newTestLimiter(t, 5, 1)
	now := time.Unix(0, 0)

	ok, err := l.Allow("a", -1, now)
	if ok || !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("negative: ok=%v err=%v", ok, err)
	}
	ok, err = l.Allow("a", 6, now)
	if ok || !errors.Is(err, ErrRequestExceedsCapacity) {
		t.Fatalf("oversize: ok=%v err=%v", ok, err)
	}
	// Capacity itself is legal.
	if ok, err := l.Allow("a", 5, now); !ok || err != nil {
		t.Fatalf("capacity-sized: ok=%v err=%v", ok, err)
	}
	// Error categories must be mutually distinct.
	if errors.Is(ErrInvalidRequest, ErrInsufficientTokens) {
		t.Fatal("invalid request must not match insufficient tokens")
	}
}
