package ontology

import (
	"sync"
	"time"
)

// unitsPerToken is the fixed-point scale of the bucket's internal
// representation: one token is exactly 1e9 units. Because one second is
// exactly 1e9 nanoseconds, a refill rate of R tokens/second is exactly R
// units/nanosecond, so the refill amount for any elapsed time.Duration is
// the exact integer product rate*elapsedNanos. Fractional tokens (e.g. 0.7
// tokens after 100ms at 7 tokens/s) are therefore accumulated exactly and
// never truncated, with no floating point anywhere in the hot path.
const unitsPerToken int64 = 1_000_000_000

// maxTokens bounds capacity and rate so that capacity*unitsPerToken and
// all refill/wait arithmetic stay well inside int64 range.
const maxTokens = int64(9_223_372_036) // floor(math.MaxInt64 / unitsPerToken)

// bucket holds the token state of a single tenant. All fields are guarded
// by mu, so a bucket is safe for concurrent use on its own.
type bucket struct {
	mu sync.Mutex
	// avail is the currently available tokens, in fixed-point units.
	avail int64
	// last is the timestamp of the last refill. It never moves backward:
	// a call with an earlier timestamp is treated as zero elapsed time.
	last time.Time
	// lastAccess is the most recent timestamp passed to Allow. It drives
	// idle reclamation and likewise never moves backward.
	lastAccess time.Time
}

// refilledUnits computes the available units after advancing the bucket
// clock from last to now, saturating at capacityUnits. It is pure: the
// caller decides whether to commit the result.
//
// Time semantics, fixed here and covered by tests:
//   - now equal to last is a no-op (zero elapsed), making same-instant
//     calls idempotent;
//   - now earlier than last (clock moving backward) is also treated as
//     zero elapsed: no tokens are added or removed and the bucket clock
//     is not moved backward;
//   - the result never exceeds capacityUnits: surplus refill is dropped.
func refilledUnits(avail, capacityUnits, rate int64, last, now time.Time) int64 {
	if !now.After(last) || rate <= 0 {
		return avail
	}
	deficit := capacityUnits - avail
	if deficit <= 0 {
		return capacityUnits
	}
	elapsed := int64(now.Sub(last)) // nanoseconds, > 0 here
	// Nanoseconds needed to fill the bucket, rounded up. Computed with
	// division instead of deficit+rate-1 so it cannot overflow.
	fillNanos := deficit / rate
	if deficit%rate != 0 {
		fillNanos++
	}
	if elapsed >= fillNanos {
		return capacityUnits
	}
	// elapsed < fillNanos guarantees rate*elapsed < deficit, so the
	// multiplication and the addition are both overflow-free.
	return avail + rate*elapsed
}

// refill advances the bucket clock to now and commits the refilled amount.
// The bucket clock only moves forward.
func (b *bucket) refill(capacityUnits, rate int64, now time.Time) {
	b.avail = refilledUnits(b.avail, capacityUnits, rate, b.last, now)
	if now.After(b.last) {
		b.last = now
	}
}
