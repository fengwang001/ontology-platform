package ontology

import (
	"testing"
	"time"
)

func TestFractionalRefillAccumulatesAcrossCalls(t *testing.T) {
	now := time.Unix(0, 0)
	capacity := int64(10)
	rate := int64(7)
	step := 100 * time.Millisecond

	fractional := newBucket(now, capacity)
	fractional.tokensScaled = 0
	fractional.refill(now.Add(step), capacity, rate)
	if fractional.tokensScaled != tokensToScaled(7)/10 {
		t.Fatalf("0.7 token remainder was lost: %d", fractional.tokensScaled)
	}

	for i := 0; i < 9; i++ {
		fractional.refill(fractional.lastTime.Add(step), capacity, rate)
	}

	oneShot := newBucket(now, capacity)
	oneShot.tokensScaled = 0
	oneShot.refill(now.Add(time.Second), capacity, rate)

	if fractional.tokensScaled != oneShot.tokensScaled {
		t.Fatalf("fractional = %d, one-shot = %d", fractional.tokensScaled, oneShot.tokensScaled)
	}
}

func TestThousandMicroAdvancesMatchOneSecond(t *testing.T) {
	now := time.Unix(0, 0)
	capacity := int64(1000)
	rate := int64(7)

	incremental := newBucket(now, capacity)
	incremental.tokensScaled = 0
	for i := 0; i < 1000; i++ {
		incremental.refill(incremental.lastTime.Add(time.Millisecond), capacity, rate)
	}

	oneShot := newBucket(now, capacity)
	oneShot.tokensScaled = 0
	oneShot.refill(now.Add(time.Second), capacity, rate)

	if incremental.tokensScaled != oneShot.tokensScaled {
		t.Fatalf("1000 advances = %d, one advance = %d", incremental.tokensScaled, oneShot.tokensScaled)
	}
	if incremental.available() != rate {
		t.Fatalf("available = %d, want %d", incremental.available(), rate)
	}
}

func TestRefillNeverExceedsCapacity(t *testing.T) {
	now := time.Unix(0, 0)
	bucketValue := newBucket(now, 3)
	bucketValue.refill(now.Add(time.Hour), 3, 5)
	if bucketValue.available() != 3 {
		t.Fatalf("available = %d, want 3", bucketValue.available())
	}
}

func TestEqualTimestampIsIdempotent(t *testing.T) {
	now := time.Unix(0, 0)
	bucketValue := newBucket(now, 5)
	bucketValue.tokensScaled = tokensToScaled(2)

	bucketValue.refill(now, 5, 5)
	first := bucketValue.tokensScaled
	bucketValue.refill(now, 5, 5)
	second := bucketValue.tokensScaled

	if first != second || first != tokensToScaled(2) {
		t.Fatalf("refill at equal timestamps changed state: %d -> %d", first, second)
	}
}
