package ontology

import (
	"testing"
	"time"
)

func TestFractionalRefillAccumulatesWithoutTruncation(t *testing.T) {
	base := time.Unix(100, 0)
	limiter := testLimiter(t, Config{Rate: 7, Capacity: 10})
	assertNoError(t, limiter.Allow("tenant", 10, base))

	for step := 1; step <= 10; step++ {
		now := base.Add(time.Duration(step) * 100 * time.Millisecond)
		want := 0
		if step >= 2 {
			want = step * 7 / 10
		}
		assertAvailable(t, limiter, "tenant", now, want)
	}

	assertAvailable(t, limiter, "tenant", base.Add(time.Second), 7)
}

func TestThousandMicroAdvancesMatchOneLargeAdvance(t *testing.T) {
	base := time.Unix(200, 0)
	stepwise := testLimiter(t, Config{Rate: 7, Capacity: 100})
	oneShot := testLimiter(t, Config{Rate: 7, Capacity: 100})
	assertNoError(t, stepwise.Allow("tenant", 100, base))
	assertNoError(t, oneShot.Allow("tenant", 100, base))

	now := base
	for range 1000 {
		now = now.Add(time.Millisecond)
		_ = stepwise.Available("tenant", now)
	}

	stepwiseTokens := stepwise.Available("tenant", now)
	oneShotTokens := oneShot.Available("tenant", base.Add(time.Second))
	if stepwiseTokens != oneShotTokens {
		t.Fatalf("stepwise = %d tokens, one-shot = %d tokens", stepwiseTokens, oneShotTokens)
	}
	if stepwiseTokens != 7 {
		t.Fatalf("refilled tokens = %d, want 7", stepwiseTokens)
	}
}

func TestRefillNeverExceedsCapacity(t *testing.T) {
	base := time.Unix(300, 0)
	limiter := testLimiter(t, Config{Rate: 10, Capacity: 5})
	assertNoError(t, limiter.Allow("tenant", 5, base))
	assertAvailable(t, limiter, "tenant", base.Add(time.Minute), 5)
}
