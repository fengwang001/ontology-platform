package ontology

import (
	"errors"
	"testing"
	"time"
)

// The reported RetryAfter must match the true refill time exactly: one
// nanosecond early still fails, at the reported time the request succeeds.
func TestRetryAfterIsExact(t *testing.T) {
	t0 := time.Unix(0, 0)
	setup := func() (*Limiter, *InsufficientTokensError) {
		l := newTestLimiter(t, 5, 2)
		// Take 4: 1 whole token left; asking 3 needs 2 more at 2/s.
		if _, err := l.Allow("a", 4, t0); err != nil {
			t.Fatal(err)
		}
		_, err := l.Allow("a", 3, t0)
		var ite *InsufficientTokensError
		if !errors.As(err, &ite) {
			t.Fatalf("err=%v", err)
		}
		return l, ite
	}

	_, ite := setup()
	if ite.Available != 1 || ite.Requested != 3 || ite.RetryAfter != time.Second {
		t.Fatalf("fields: %+v, want available=1 requested=3 retry=1s", ite)
	}

	early, _ := setup()
	ok, err := early.Allow("a", 3, t0.Add(ite.RetryAfter-time.Nanosecond))
	if ok || !errors.Is(err, ErrInsufficientTokens) {
		t.Fatalf("one ns early: ok=%v err=%v", ok, err)
	}

	exact, _ := setup()
	ok, err = exact.Allow("a", 3, t0.Add(ite.RetryAfter))
	if !ok || err != nil {
		t.Fatalf("at retry-after: ok=%v err=%v", ok, err)
	}
}

// Fractional waits must round up to a whole nanosecond, never down.
func TestRetryAfterRoundsUp(t *testing.T) {
	l := newTestLimiter(t, 10, 3)
	t0 := time.Unix(0, 0)
	if _, err := l.Allow("a", 10, t0); err != nil {
		t.Fatal(err)
	}
	_, err := l.Allow("a", 1, t0)
	var ite *InsufficientTokensError
	if !errors.As(err, &ite) {
		t.Fatal(err)
	}
	// 1 token at 3/s = 333333333.333...ns => 333333334ns.
	want := 333_333_334 * time.Nanosecond
	if ite.RetryAfter != want {
		t.Fatalf("retry=%v, want %v", ite.RetryAfter, want)
	}
}
