package ontology

import (
	"testing"
	"time"
)

func TestRewoundTimeRejectsCallAndPreservesBucket(t *testing.T) {
	base := time.Unix(400, 0)
	limiter := testLimiter(t, Config{Rate: 5, Capacity: 3})
	assertNoError(t, limiter.Allow("tenant", 3, base))

	after := base.Add(time.Second)
	assertAvailable(t, limiter, "tenant", after, 3)
	assertErrorIs(t, limiter.Allow("tenant", 1, base), ErrTimeRewound)

	assertAvailable(t, limiter, "tenant", after, 3)
	assertNoError(t, limiter.Allow("tenant", 1, after))
	assertAvailable(t, limiter, "tenant", after, 2)
}

func TestEqualTimestampAddsNoRefill(t *testing.T) {
	base := time.Unix(500, 0)
	limiter := testLimiter(t, Config{Rate: 1000, Capacity: 3})
	assertNoError(t, limiter.Allow("tenant", 3, base))

	assertInsufficient(t, limiter.Allow("tenant", 1, base))
	assertAvailable(t, limiter, "tenant", base, 0)
}

func TestZeroRequestAtEqualTimestampIsAlwaysAllowed(t *testing.T) {
	base := time.Unix(600, 0)
	limiter := testLimiter(t, Config{Rate: 1, Capacity: 1})
	assertNoError(t, limiter.Allow("tenant", 1, base))

	assertNoError(t, limiter.Allow("tenant", 0, base))
	assertAvailable(t, limiter, "tenant", base, 0)
}
