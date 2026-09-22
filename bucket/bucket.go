// Package bucket implements a single token bucket refilled by an
// injected clock. It has no dependencies on other packages.
package bucket

import (
	"errors"
	"time"
)

// ErrInsufficient is returned when the bucket does not hold enough
// tokens to satisfy a deduction.
var ErrInsufficient = errors.New("bucket: insufficient tokens")

// ErrInvalidAmount is returned when a deduction amount is not positive.
var ErrInvalidAmount = errors.New("bucket: amount must be positive")

// ErrExceedsCapacity is returned when a single deduction amount is
// larger than the bucket capacity; it can never succeed.
var ErrExceedsCapacity = errors.New("bucket: amount exceeds capacity")

// Bucket is a token bucket refilled continuously at a fixed rate.
// It is not safe for concurrent use; callers must synchronize.
type Bucket struct {
	capacity float64
	rate     float64 // tokens per second
	tokens   float64
	last     time.Time
	now      func() time.Time
}

// New creates a full bucket with the given capacity and refill rate.
// now supplies the current time; if it ever moves backwards the
// bucket treats it as zero elapsed time.
func New(capacity, rate float64, now func() time.Time) *Bucket {
	return &Bucket{
		capacity: capacity,
		rate:     rate,
		tokens:   capacity,
		last:     now(),
		now:      now,
	}
}

// refill advances the bucket to the injected current time. A clock
// that moved backwards is treated as zero elapsed time.
func (b *Bucket) refill() {
	now := b.now()
	if now.After(b.last) {
		elapsed := now.Sub(b.last).Seconds()
		b.tokens += b.rate * elapsed
		if b.tokens > b.capacity {
			b.tokens = b.capacity
		}
		b.last = now
	}
}

// TryTake attempts to deduct n tokens. On success it returns nil and
// the tokens are deducted. On failure the bucket is left exactly as
// it was before the call (aside from time-based refill).
func (b *Bucket) TryTake(n float64) error {
	if n <= 0 {
		return ErrInvalidAmount
	}
	if n > b.capacity {
		return ErrExceedsCapacity
	}
	b.refill()
	if b.tokens < n {
		return ErrInsufficient
	}
	b.tokens -= n
	return nil
}

// GiveBack returns n previously deducted tokens, capped at capacity.
// It is used to roll back a partial two-level deduction.
func (b *Bucket) GiveBack(n float64) {
	if n <= 0 {
		return
	}
	b.tokens += n
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
}

// Balance reports the current token count after applying time-based
// refill. It does not consume tokens.
func (b *Bucket) Balance() float64 {
	b.refill()
	return b.tokens
}

// Reconfigure updates capacity and rate in place. The balance is
// truncated when capacity shrinks, but never topped up when capacity
// grows. Reconfiguring neither consumes nor grants tokens.
func (b *Bucket) Reconfigure(capacity, rate float64) {
	b.refill()
	b.capacity = capacity
	b.rate = rate
	if b.tokens > capacity {
		b.tokens = capacity
	}
}

// Capacity reports the current capacity.
func (b *Bucket) Capacity() float64 {
	return b.capacity
}

// Rate reports the current refill rate in tokens per second.
func (b *Bucket) Rate() float64 {
	return b.rate
}
