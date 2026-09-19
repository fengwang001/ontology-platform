package ontology

import "time"

// bucket holds the token-bucket state of a single tenant.
//
// Token amounts are integer nanotokens (see scale in math.go): the integer
// part is tokens/scale and the remainder is the fractional token carried
// forward. A bucket therefore never loses sub-token refill fractions.
//
// A bucket does not own a mutex; its owning shard holds the lock while any
// bucket method runs.
type bucket struct {
	capacity int64 // capacity in nanotokens
	rate     int64 // tokens per second (integer)
	tokens   int64 // currently available nanotokens, 0 <= tokens <= capacity
	// last is the timestamp of the most recent accepted call. The zero
	// value time.Time{} marks a bucket that has never been used; the first
	// call treats it as starting full at that timestamp.
	last time.Time
}

func newBucket(capacityTokens, rate int64) *bucket {
	return &bucket{
		capacity: capacityTokens * scale,
		rate:     rate,
		tokens:   capacityTokens * scale, // tenants start with a full bucket
	}
}

// advance refills the bucket up to capacity according to the elapsed time
// since the last accepted call and records now as the new timestamp.
//
// Callers must have already validated that now is not earlier than b.last.
// Equal timestamps add zero tokens. Refill beyond capacity is discarded.
func (b *bucket) advance(now time.Time) {
	if !b.last.IsZero() {
		elapsed := now.Sub(b.last).Nanoseconds() // >= 0 by construction
		if elapsed > 0 && b.tokens < b.capacity {
			// Any refill beyond full is discarded anyway, so clamp the
			// elapsed time to exactly the time needed to fill up. This
			// also keeps every intermediate product inside int64.
			fill := waitNanos(b.capacity-b.tokens, b.rate)
			if elapsed > fill {
				elapsed = fill
			}
			b.tokens += refillNanotokens(b.rate, elapsed)
			if b.tokens > b.capacity {
				b.tokens = b.capacity
			}
		}
	}
	b.last = now
}

// allow applies the request-time validation rules shared by Allow and
// Available. It returns the refill-adjusted bucket state.
//
// n validation is performed before any time validation, so that bad
// parameters are reported even for a reversed clock.
func (b *bucket) allow(n int64, now time.Time) (bool, error) {
	if n < 0 {
		return false, ErrInvalidRequest
	}
	// Compare without multiplying to avoid overflow on a huge (but
	// otherwise positive) n.
	if n > b.capacity/scale {
		return false, ErrRequestExceedsCapacity
	}
	if !b.last.IsZero() && now.Before(b.last) {
		// Deterministic time-reversal policy: reject, mutate nothing.
		return false, ErrTimeReversed
	}

	b.advance(now)

	// A zero-sized request is always allowed and consumes nothing, so it is
	// idempotent even when called repeatedly at the same timestamp.
	need := n * scale // safe: n <= capacity/scale was validated above
	if need == 0 {
		return true, nil
	}
	if b.tokens >= need {
		b.tokens -= need
		return true, nil
	}

	missing := need - b.tokens // nanotokens still required
	return false, &InsufficientTokensError{
		Requested:  n,
		Available:  b.tokens / scale,
		RetryAfter: time.Duration(waitNanos(missing, b.rate)),
	}
}

// available returns the whole number of tokens available after refilling up
// to now. Fractional tokens are retained internally but not reported.
// A timestamp earlier than the last call yields ErrTimeReversed without
// mutating any state.
func (b *bucket) available(now time.Time) (int64, error) {
	if !b.last.IsZero() && now.Before(b.last) {
		return 0, ErrTimeReversed
	}
	b.advance(now)
	return b.tokens / scale, nil
}
