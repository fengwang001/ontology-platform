package ontology

import (
	"time"
)

const tokenScale = int64(time.Second)

type bucket struct {
	available int64
	capacity  int
	rate      int
	last      time.Time
}

func newBucket(capacity, rate int, now time.Time) *bucket {
	return &bucket{
		available: int64(capacity) * tokenScale,
		capacity:  capacity,
		rate:      rate,
		last:      now,
	}
}

func (b *bucket) refill(now time.Time) {
	elapsed := now.Sub(b.last)
	if elapsed <= 0 {
		return
	}

	full := int64(b.capacity) * tokenScale
	missing := full - b.available
	elapsedNanoseconds := elapsed.Nanoseconds()
	if elapsedNanoseconds >= ceilDiv(missing, int64(b.rate)) {
		b.available = full
		b.last = now
		return
	}

	b.available += elapsedNanoseconds * int64(b.rate)
	b.last = now
	if b.available > full {
		b.available = full
	}
}

func (b *bucket) tokens() int {
	return int(b.available / tokenScale)
}

func (b *bucket) waitFor(n int, now time.Time) time.Duration {
	b.refill(now)
	required := int64(n) * tokenScale
	missing := required - b.available
	if missing <= 0 {
		return 0
	}

	nanoseconds := ceilDiv(missing, int64(b.rate))
	return time.Duration(nanoseconds)
}

func ceilDiv(value, divisor int64) int64 {
	return (value + divisor - 1) / divisor
}
