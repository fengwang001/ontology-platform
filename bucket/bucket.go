// Package bucket implements a single token bucket driven by an injected clock.
package bucket

import "time"

// Clock returns the current time. Injected so tests control time.
type Clock func() time.Time

// Bucket is a token bucket refilled continuously at rate tokens per second,
// capped at capacity. It is not safe for concurrent use; callers must
// serialize access.
type Bucket struct {
	capacity float64
	rate     float64
	tokens   float64
	last     time.Time
	now      Clock
}

// New returns a full bucket with the given capacity and refill rate.
func New(capacity, rate float64, now Clock) *Bucket {
	return &Bucket{
		capacity: capacity,
		rate:     rate,
		tokens:   capacity,
		last:     now(),
		now:      now,
	}
}

// refill advances the bucket to now(). A clock that moves backwards is
// treated as no elapsed time; tokens are never deducted by time travel.
func (b *Bucket) refill() {
	now := b.now()
	if now.After(b.last) {
		b.tokens += b.rate * now.Sub(b.last).Seconds()
		if b.tokens > b.capacity {
			b.tokens = b.capacity
		}
		b.last = now
	}
}

// TryTake deducts n tokens if enough are available.
func (b *Bucket) TryTake(n float64) bool {
	b.refill()
	if n > b.tokens {
		return false
	}
	b.tokens -= n
	return true
}

// Refund returns n tokens, capped at capacity. It rolls back a prior
// successful TryTake.
func (b *Bucket) Refund(n float64) {
	b.tokens += n
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
}

// Balance reports the current token count after refilling to now.
func (b *Bucket) Balance() float64 {
	b.refill()
	return b.tokens
}

// Capacity reports the burst limit.
func (b *Bucket) Capacity() float64 {
	return b.capacity
}

// SetLimit changes capacity and rate without refilling or consuming tokens.
// A smaller capacity truncates the stored balance; a larger one leaves it.
func (b *Bucket) SetLimit(capacity, rate float64) {
	b.capacity = capacity
	b.rate = rate
	if b.tokens > capacity {
		b.tokens = capacity
	}
}
