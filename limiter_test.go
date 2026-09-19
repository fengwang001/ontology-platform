package ontology

import (
	"errors"
	"testing"
	"time"
)

func TestNewLimiterRequiresPositiveValues(t *testing.T) {
	for _, values := range [][2]int64{{0, 1}, {1, 0}, {-1, 1}, {1, -1}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("NewLimiter(%d, %d) did not panic", values[0], values[1])
				}
			}()
			NewLimiter(values[0], values[1])
		}()
	}
}

func TestRequestBoundaries(t *testing.T) {
	now := time.Unix(0, 0)
	limiter := NewLimiter(5, 5)

	allowed, err := limiter.Allow("tenant", 0, now)
	if err != nil || !allowed {
		t.Fatalf("n=0 = (%v, %v), want (true, nil)", allowed, err)
	}
	if got, _ := limiter.Available("tenant", now); got != 5 {
		t.Fatalf("n=0 consumed tokens: available = %d", got)
	}

	allowed, err = limiter.Allow("tenant", -1, now.Add(time.Second))
	if allowed || !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("negative n = (%v, %v), want ErrInvalidRequest", allowed, err)
	}

	allowed, err = limiter.Allow("tenant", 6, now.Add(2*time.Second))
	if allowed || !errors.Is(err, ErrRequestTooLarge) {
		t.Fatalf("oversized n = (%v, %v), want ErrRequestTooLarge", allowed, err)
	}
}

func TestTimeReversalIsRejectedAndPreservesBucket(t *testing.T) {
	start := time.Unix(0, 0)
	next := start.Add(time.Second)
	limiter := NewLimiter(10, 10)

	if _, err := limiter.Allow("tenant", 8, start); err != nil {
		t.Fatal(err)
	}
	allowed, err := limiter.Allow("tenant", 5, start.Add(-time.Second))
	if allowed || !errors.Is(err, ErrTimeReversed) {
		t.Fatalf("reversed time = (%v, %v), want ErrTimeReversed", allowed, err)
	}
	if got, err := limiter.Available("tenant", start.Add(-time.Millisecond)); !errors.Is(err, ErrTimeReversed) || got != 2 {
		t.Fatalf("reversed query = (%d, %v), want 2 and ErrTimeReversed", got, err)
	}
	if _, err := limiter.Allow("tenant", 2, next); err != nil {
		t.Fatalf("bucket state after reversed calls changed: %v", err)
	}
}

func TestRetryAfterIsExactToNanosecond(t *testing.T) {
	start := time.Unix(0, 0)
	limiter := NewLimiter(10, 7)

	if _, err := limiter.Allow("tenant", 10, start); err != nil {
		t.Fatal(err)
	}
	allowed, err := limiter.Allow("tenant", 10, start)
	if allowed || err == nil {
		t.Fatal("empty bucket unexpectedly allowed request")
	}

	var insufficient *InsufficientError
	if !errors.As(err, &insufficient) || insufficient.Available != 0 || insufficient.Requested != 10 {
		t.Fatalf("err = %#v, want InsufficientError", err)
	}

	retryAt := start.Add(insufficient.RetryAfter)
	if allowed, err := limiter.Allow("tenant", 10, retryAt.Add(-time.Nanosecond)); allowed || !errors.As(err, new(*InsufficientError)) {
		t.Fatalf("one nanosecond early = (%v, %v), want rejection", allowed, err)
	}
	if allowed, err := limiter.Allow("tenant", 10, retryAt); !allowed || err != nil {
		t.Fatalf("at retry time = (%v, %v), want allow", allowed, err)
	}
}

func TestTenantsAreIsolated(t *testing.T) {
	now := time.Unix(0, 0)
	limiter := NewLimiter(3, 3)

	if _, err := limiter.Allow("a", 3, now); err != nil {
		t.Fatal(err)
	}
	if got, err := limiter.Available("a", now); err != nil || got != 0 {
		t.Fatalf("tenant a = %d, want 0", got)
	}
	if got, err := limiter.Available("b", now); err != nil || got != 3 {
		t.Fatalf("tenant b = %d, want full bucket", got)
	}
}
