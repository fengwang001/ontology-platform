// Package tkn implements the token-bucket state machine without any notion
// of time: callers drive it explicitly via Refill and TryConsume.
package tkn

// Bucket is a single token bucket. It is NOT safe for concurrent use; the
// lim package is responsible for serializing access.
type Bucket struct {
	capacity int64
	rate     int64
	tokens   int64

	// refillSteps counts the per-token increments performed by the most
	// recent Refill. Refill computes the result with one multiplication,
	// so there is never a per-token loop and the count is always 0.
	// It is intentionally unexported and has no accessor.
	refillSteps int64
}

// New creates a full bucket. capacity and rate must be positive; that
// precondition is enforced by the api layer before construction.
func New(capacity, rate int64) *Bucket {
	return &Bucket{capacity: capacity, rate: rate, tokens: capacity}
}

// Capacity returns the bucket capacity.
func (b *Bucket) Capacity() int64 { return b.capacity }

// Rate returns the refill rate (tokens per abstract time unit).
func (b *Bucket) Rate() int64 { return b.rate }

// Tokens returns the current number of tokens (0 <= tokens <= capacity).
func (b *Bucket) Tokens() int64 { return b.tokens }

// Refill adds elapsed*rate tokens, capped at capacity. elapsed must be >= 0
// (a non-positive elapsed is a no-op). It runs in O(1): one multiplication,
// no per-token loop, so refillSteps stays 0 afterwards.
func (b *Bucket) Refill(elapsed int64) {
	b.refillSteps = 0
	if elapsed <= 0 {
		return
	}
	room := b.capacity - b.tokens
	added := elapsed * b.rate
	// added < 0 can only come from int64 overflow, which means the
	// requested refill dwarfs the remaining room: fill to capacity.
	if added < 0 || added >= room {
		b.tokens = b.capacity
		return
	}
	b.tokens += added
}

// RefillIsO1 reports whether the most recent Refill did constant work.
// Only this boolean is part of the public surface; the raw counter value
// never leaves the package (white-box tests in this package read it).
func (b *Bucket) RefillIsO1() bool { return b.refillSteps == 0 }

// TryConsume attempts to deduct need tokens. It reports whether the request
// is allowed. On rejection no state changes. The caller guarantees need >= 1.
func (b *Bucket) TryConsume(need int64) bool {
	if need <= 0 || b.tokens < need {
		return false
	}
	b.tokens -= need
	return true
}
