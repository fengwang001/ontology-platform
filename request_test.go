package ontology

import (
	"testing"
	"time"
)

func TestZeroNegativeAndOverCapacityRequests(t *testing.T) {
	base := time.Unix(700, 0)
	limiter := testLimiter(t, Config{Rate: 7, Capacity: 5})

	assertNoError(t, limiter.Allow("tenant", 0, base))
	assertNoError(t, limiter.Allow("tenant", 5, base))
	assertNoError(t, limiter.Allow("tenant", 0, base))
	assertAvailable(t, limiter, "tenant", base, 0)

	assertErrorIs(t, limiter.Allow("tenant", -1, base), ErrNegativeTokens)
	assertErrorIs(t, limiter.Allow("tenant", 6, base), ErrRequestExceedsCapacity)

	assertAvailable(t, limiter, "tenant", base, 0)
	assertNoError(t, limiter.Allow("tenant", 1, base.Add(143*time.Millisecond)))
}

func TestInsufficientWaitMatchesActualRefill(t *testing.T) {
	base := time.Unix(800, 0)
	limiter := testLimiter(t, Config{Rate: 7, Capacity: 5})
	assertNoError(t, limiter.Allow("tenant", 5, base))

	rejectedAt := base.Add(100 * time.Millisecond)
	err := assertInsufficient(t, limiter.Allow("tenant", 3, rejectedAt))
	if err.RetryAfter != 328571429*time.Nanosecond {
		t.Fatalf("RetryAfter = %d, want 328571429ns", err.RetryAfter)
	}
	if err.Available != 0 || err.Requested != 3 {
		t.Fatalf("error metadata = %+v", err)
	}

	oneUnitBefore := rejectedAt.Add(err.RetryAfter - time.Nanosecond)
	assertInsufficient(t, limiter.Allow("tenant", 3, oneUnitBefore))

	readyAt := rejectedAt.Add(err.RetryAfter)
	assertNoError(t, limiter.Allow("tenant", 3, readyAt))
}

func TestInvalidConfigRejected(t *testing.T) {
	invalid := []Config{
		{Rate: 0, Capacity: 1},
		{Rate: 1, Capacity: 0},
		{Rate: -1, Capacity: 1},
		{Rate: 1, Capacity: 1, IdleTTL: -1},
	}
	for _, cfg := range invalid {
		if _, err := NewLimiter(cfg); err != ErrInvalidConfig {
			t.Fatalf("NewLimiter(%+v) error = %v", cfg, err)
		}
	}
}
