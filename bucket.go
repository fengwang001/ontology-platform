package ontology

import (
	"math"
	"sync"
	"time"
)

const nanosPerSecond = int64(time.Second)

type bucket struct {
	mu           sync.Mutex
	tokensScaled int64
	lastTime     time.Time
	lastAccess   time.Time
}

func newBucket(now time.Time, capacity int64) *bucket {
	return &bucket{
		tokensScaled: tokensToScaled(capacity),
		lastTime:     now,
		lastAccess:   now,
	}
}

func (b *bucket) refill(now time.Time, capacity, rate int64) {
	if now.Before(b.lastTime) || now.Equal(b.lastTime) {
		b.lastAccess = now
		return
	}

	elapsed := now.Sub(b.lastTime).Nanoseconds()
	added := saturatedMultiply(elapsed, rate)
	capacityScaled := tokensToScaled(capacity)
	b.tokensScaled = saturatedAdd(b.tokensScaled, added)
	if b.tokensScaled > capacityScaled {
		b.tokensScaled = capacityScaled
	}
	b.lastTime = now
	b.lastAccess = now
}

func (b *bucket) available() int64 {
	return b.tokensScaled / nanosPerSecond
}

func (b *bucket) allow(n int64, now time.Time, capacity, rate int64) (bool, error) {
	if n < 0 {
		return false, ErrInvalidRequest
	}
	if n > capacity {
		return false, ErrRequestTooLarge
	}
	if now.Before(b.lastTime) {
		return false, ErrTimeReversed
	}

	b.refill(now, capacity, rate)
	requestedScaled := tokensToScaled(n)
	if b.tokensScaled >= requestedScaled {
		b.tokensScaled -= requestedScaled
		return true, nil
	}

	deficitScaled := requestedScaled - b.tokensScaled
	return false, &InsufficientError{
		Available:  b.available(),
		Requested:  n,
		RetryAfter: waitDuration(deficitScaled, rate),
	}
}

func tokensToScaled(tokens int64) int64 {
	return saturatedMultiply(tokens, nanosPerSecond)
}

func waitDuration(deficitScaled, rate int64) time.Duration {
	if deficitScaled <= 0 || rate <= 0 {
		return 0
	}
	waitNanos := deficitScaled / rate
	if deficitScaled%rate != 0 {
		waitNanos++
	}
	return time.Duration(waitNanos)
}

func saturatedAdd(a, b int64) int64 {
	if b > math.MaxInt64-a {
		return math.MaxInt64
	}
	return a + b
}

func saturatedMultiply(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}
