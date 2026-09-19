package ontology

import (
	"errors"
	"math"
	"testing"
	"time"
)

// n == 0 is always allowed and consumes nothing.
func TestZeroN(t *testing.T) {
	l := mustLimiter(t, 10, 5)
	base := time.Unix(6_000, 0)
	drain(t, l, "t", base)
	d, err := l.Allow("t", 0, base)
	if err != nil || !d.Allowed || d.Wait != 0 {
		t.Fatalf("Allow(0): %+v err=%v, want allowed with zero wait", d, err)
	}
	if got := l.Available("t", base); got != 0 {
		t.Fatalf("Allow(0) consumed tokens: available=%d, want 0", got)
	}
}

// n < 0 is an argument error, a different class from insufficient tokens,
// and leaves the bucket untouched.
func TestNegativeN(t *testing.T) {
	l := mustLimiter(t, 10, 5)
	base := time.Unix(7_000, 0)
	d, err := l.Allow("t", -1, base)
	if !errors.Is(err, ErrNegativeN) {
		t.Fatalf("Allow(-1): err=%v, want ErrNegativeN", err)
	}
	if d.Allowed {
		t.Fatal("Allow(-1) must not be allowed")
	}
	if got := l.Available("t", base); got != 10 {
		t.Fatalf("Allow(-1) changed bucket: available=%d, want 10", got)
	}
	// Insufficient tokens is not an error at all.
	drain(t, l, "t", base)
	d, err = l.Allow("t", 1, base)
	if err != nil || d.Allowed {
		t.Fatalf("empty bucket: %+v err=%v, want rejected without error", d, err)
	}
}

// n greater than capacity fails immediately with a distinguishable
// "can never be satisfied" error instead of waiting forever.
func TestExceedsCapacity(t *testing.T) {
	l := mustLimiter(t, 10, 5)
	base := time.Unix(8_000, 0)
	d, err := l.Allow("t", 11, base)
	if !errors.Is(err, ErrExceedsCapacity) {
		t.Fatalf("Allow(11) with capacity 10: err=%v, want ErrExceedsCapacity", err)
	}
	if d.Allowed {
		t.Fatal("Allow(11) must not be allowed")
	}
	if got := l.Available("t", base); got != 10 {
		t.Fatalf("rejected oversized request consumed tokens: available=%d", got)
	}
}

// The reported wait matches the real refill time within one nanosecond,
// and after exactly that wait the request succeeds.
func TestWaitDurationIsAccurate(t *testing.T) {
	l := mustLimiter(t, 10, 3)
	base := time.Unix(9_000, 0)
	drain(t, l, "t", base)

	d, err := l.Allow("t", 4, base)
	if err != nil || d.Allowed {
		t.Fatalf("Allow(4) on empty bucket: %+v err=%v", d, err)
	}
	// 4 tokens at 3/s: 4e6*1e9/3e6 = 1333333333.33.. ns, rounded up.
	want := time.Duration(1_333_333_334)
	if d.Wait != want {
		t.Fatalf("wait=%v, want %v", d.Wait, want)
	}
	// One nanosecond early is still not enough.
	if d2, _ := l.Allow("t", 4, base.Add(d.Wait-time.Nanosecond)); d2.Allowed {
		t.Fatal("request allowed one nanosecond before the reported wait")
	}
	// Exactly at the reported wait it succeeds.
	if d2, _ := l.Allow("t", 4, base.Add(d.Wait)); !d2.Allowed {
		t.Fatal("request rejected after waiting the reported duration")
	}
}

// With a zero rate a deficit can never refill; the wait saturates.
func TestWaitWithZeroRate(t *testing.T) {
	l := mustLimiter(t, 10, 0)
	base := time.Unix(10_000, 0)
	drain(t, l, "t", base)
	d, err := l.Allow("t", 1, base)
	if err != nil || d.Allowed {
		t.Fatalf("Allow(1): %+v err=%v", d, err)
	}
	if d.Wait != time.Duration(math.MaxInt64) {
		t.Fatalf("zero-rate wait=%v, want MaxInt64", d.Wait)
	}
}

// A rejected request consumes nothing.
func TestRejectionConsumesNothing(t *testing.T) {
	l := mustLimiter(t, 10, 0)
	base := time.Unix(11_000, 0)
	if d, _ := l.Allow("t", 6, base); !d.Allowed {
		t.Fatal("setup: first Allow(6) should pass")
	}
	if d, _ := l.Allow("t", 6, base); d.Allowed {
		t.Fatal("setup: second Allow(6) should be rejected")
	}
	if got := l.Available("t", base); got != 4 {
		t.Fatalf("available=%d after rejection, want 4", got)
	}
}
