package ontology

import (
	"math"
	"math/bits"
	"time"
)

// micro is the fixed-point scale: one token is micro sub-units. All token
// bookkeeping happens in these integer units, so fractional refills are
// exact and never drift.
const micro = 1_000_000

// maxTokens bounds capacity and rate so that token counts scaled by micro
// always fit in an int64.
const maxTokens = math.MaxInt64 / micro

// bucket is the per-tenant state. All fields are guarded by the owning
// shard's mutex.
type bucket struct {
	avail int64     // available tokens, in micro-tokens
	last  time.Time // last time the bucket was touched
	rem   int64     // refill remainder, in micro-token*nanoseconds
}

// newBucket returns a full bucket whose clock starts at now.
func newBucket(now time.Time, capMicro int64) *bucket {
	return &bucket{avail: capMicro, last: now}
}

// refill advances the bucket to now. A now that is earlier than or equal
// to the last call is a zero-length interval: nothing is added and last
// is never moved backwards. Refill is capped at capMicro; when the cap is
// hit the sub-token remainder is discarded along with the excess.
func (b *bucket) refill(now time.Time, rateMicro, capMicro int64) {
	if !now.After(b.last) {
		return
	}
	elapsed := now.Sub(b.last).Nanoseconds()
	b.last = now
	if rateMicro <= 0 || elapsed <= 0 {
		return
	}
	// Saturate instead of overflowing: an elapsed this large refills far
	// beyond any valid capacity.
	if elapsed > (math.MaxInt64-b.rem)/rateMicro {
		b.avail = capMicro
		b.rem = 0
		return
	}
	total := elapsed*rateMicro + b.rem
	second := int64(time.Second)
	b.avail += total / second
	b.rem = total % second
	if b.avail >= capMicro {
		b.avail = capMicro
		b.rem = 0
	}
}

// waitFor returns how long until deficitMicro micro-tokens refill at
// rateMicro micro-tokens per second, rounded up to the nanosecond so the
// reported wait is always sufficient and within one nanosecond of the
// true value. A zero rate means the wait can never elapse.
func waitFor(deficitMicro, rateMicro int64) time.Duration {
	if deficitMicro <= 0 {
		return 0
	}
	if rateMicro <= 0 {
		return time.Duration(math.MaxInt64)
	}
	hi, lo := bits.Mul64(uint64(deficitMicro), uint64(time.Second))
	if hi >= uint64(rateMicro) {
		return time.Duration(math.MaxInt64)
	}
	q, r := bits.Div64(hi, lo, uint64(rateMicro))
	if r != 0 {
		q++
	}
	if q > uint64(math.MaxInt64) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(q)
}
