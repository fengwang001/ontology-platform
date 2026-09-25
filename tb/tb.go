// Package tb implements the standalone token bucket: capacity, refill rate,
// token counting, refill by elapsed ticks and decrement. It depends on no
// other package of this module.
package tb

import "errors"

// ErrInvalidParam is returned when B <= 0 or r < 0.
var ErrInvalidParam = errors.New("tb: invalid parameters (B must be > 0, r must be >= 0)")

// Bucket is a token bucket that starts full and refills at a fixed integer
// rate per tick, capped at its capacity.
type Bucket struct {
	cap    int64
	rate   int64
	tokens int64
}

// New creates a bucket that starts full: B tokens are available immediately.
func New(B, r int) (*Bucket, error) {
	if B <= 0 || r < 0 {
		return nil, ErrInvalidParam
	}
	c := int64(B)
	return &Bucket{cap: c, rate: int64(r), tokens: c}, nil
}

// Cap reports the bucket capacity.
func (b *Bucket) Cap() int { return int(b.cap) }

// Rate reports tokens added per tick.
func (b *Bucket) Rate() int { return int(b.rate) }

// Tokens reports the currently available token count.
func (b *Bucket) Tokens() int { return int(b.tokens) }

// Refill adds rate*dt tokens accumulated over dt ticks, capped at capacity.
func (b *Bucket) Refill(dt int64) {
	if dt <= 0 || b.rate == 0 {
		return
	}
	added := b.rate * dt
	if added >= b.cap-b.tokens {
		b.tokens = b.cap
		return
	}
	b.tokens += added
}

// Take consumes exactly one token. It reports whether a token was available;
// when none is available the bucket is left untouched.
func (b *Bucket) Take() bool {
	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}

// Adjust applies a bulk signed correction to the token count (positive for a
// bulk credit, negative for bulk consumption), clamped to [0, capacity]. It
// returns the resulting count.
func (b *Bucket) Adjust(delta int64) int64 {
	if delta > 0 {
		if delta >= b.cap-b.tokens {
			b.tokens = b.cap
		} else {
			b.tokens += delta
		}
	} else if delta < 0 {
		cost := -delta
		if cost >= b.tokens {
			b.tokens = 0
		} else {
			b.tokens -= cost
		}
	}
	return b.tokens
}
