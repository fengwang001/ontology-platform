package ontology

import (
	"sync"
	"time"
)

const nanosPerSecond = int64(time.Second)

type bucket struct {
	mu        sync.Mutex
	capacity  int64 // Full tokens, scaled by nanosPerSecond.
	rate      int64 // Tokens per second, scaled by nanosPerSecond.
	available int64 // Scaled token units.
	last      time.Time
	deleted   bool
}

func newFullBucket(capacity int64, rate int64, now time.Time) *bucket {
	return &bucket{
		capacity:  capacity * nanosPerSecond,
		rate:      rate * nanosPerSecond,
		available: capacity * nanosPerSecond,
		last:      now,
	}
}

// refill applies deterministic fixed-point refill and returns true on success.
// Moving the clock backward is rejected without changing any bucket state.
func (b *bucket) refill(now time.Time) bool {
	if b.last.IsZero() {
		b.last = now
		return true
	}

	elapsed := now.Sub(b.last)
	switch {
	case elapsed < 0:
		return false
	case elapsed == 0:
		return true
	}

	missing := b.capacity - b.available
	if missing <= 0 {
		b.available = b.capacity
		b.last = now
		return true
	}

	gain := refillGain(now.Sub(b.last), b.rate, missing)
	b.available += gain
	if b.available > b.capacity {
		b.available = b.capacity
	}
	b.last = now
	return true
}

func refillGain(elapsed time.Duration, rate int64, missing int64) int64 {
	elapsedNanos := int64(elapsed)

	// A full bucket is the useful ceiling. This clamp also prevents the
	// elapsed*rate multiplication from overflowing.
	if elapsedNanos >= missing*nanosPerSecond/rate+1 {
		return missing
	}

	gain := elapsedNanos * rate / nanosPerSecond
	if gain > missing {
		return missing
	}
	return gain
}

func (b *bucket) availableTokens() int64 {
	return b.available / nanosPerSecond
}

func waitFor(missingScaled int64, rate int64) time.Duration {
	if missingScaled <= 0 {
		return 0
	}

	rateScaled := rate / nanosPerSecond
	// Quotient and remainder avoid overflowing when taking the ceiling.
	wait := time.Duration(missingScaled / rateScaled)
	if missingScaled%rateScaled != 0 {
		wait++
	}
	return wait
}
