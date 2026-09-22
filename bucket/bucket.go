// Package bucket implements a single token bucket driven by an injected
// clock. It has no dependencies on other packages in this module.
package bucket

import (
	"sync"
	"time"
)

// Bucket is a token bucket refilled continuously from an injected clock.
// It is safe for concurrent use, but callers that need atomic operations
// spanning multiple buckets must serialize access themselves.
type Bucket struct {
	mu       sync.Mutex
	now      func() time.Time
	capacity float64
	rate     float64 // tokens per second
	tokens   float64
	last     time.Time
}

// New returns a full bucket with the given capacity (also the burst limit)
// and refill rate in tokens per second.
func New(capacity, ratePerSec float64, now func() time.Time) *Bucket {
	return &Bucket{
		now:      now,
		capacity: capacity,
		rate:     ratePerSec,
		tokens:   capacity,
		last:     now(),
	}
}

// refill adds tokens for the elapsed time, capped at capacity. A clock
// that moves backwards is treated as no elapsed time; tokens are never
// removed by time passing.
func (b *Bucket) refill() {
	now := b.now()
	elapsed := now.Sub(b.last)
	if elapsed <= 0 {
		return
	}
	b.last = now
	b.tokens += b.rate * elapsed.Seconds()
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
}

// TryTake attempts to remove n tokens and reports whether it succeeded.
// On failure the balance is left untouched.
func (b *Bucket) TryTake(n float64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill()
	if n > b.capacity || n > b.tokens {
		return false
	}
	b.tokens -= n
	return true
}

// Rollback returns n previously taken tokens, capped at capacity.
func (b *Bucket) Rollback(n float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tokens += n
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
}

// Balance refills from the clock and returns the current token count.
func (b *Bucket) Balance() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill()
	return b.tokens
}

// Capacity returns the bucket capacity (burst limit).
func (b *Bucket) Capacity() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.capacity
}

// Update changes capacity and rate without refilling or consuming tokens.
// Shrinking the capacity truncates the current balance; growing it leaves
// the balance unchanged.
func (b *Bucket) Update(capacity, ratePerSec float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.capacity = capacity
	b.rate = ratePerSec
	if b.tokens > capacity {
		b.tokens = capacity
	}
}
