// Package ontology implements a multi-tenant token bucket rate limiter
// using only the standard library.
//
// Each tenant gets its own bucket with a fixed capacity and a per-second
// refill rate. Time is always supplied by the caller (the now argument);
// the limiter never reads a real clock, which makes tests fully
// deterministic.
//
// Time semantics, fixed here and covered by tests:
//
//   - Refill is computed in fixed-point micro-tokens (1 token = 1e6
//     micro-tokens) with a per-bucket remainder, so fractional refills are
//     never truncated away. Advancing 1ms a thousand times yields exactly
//     the same result as advancing 1s once. No float64 is ever used.
//   - Refill never raises a bucket above its capacity; excess is dropped.
//   - A now earlier than the last call for that bucket is treated as a
//     zero-length interval: no refill happens, the bucket is not cleared,
//     no error is returned, and the bucket's last-seen time is not moved
//     backwards. A now equal to the last call also adds nothing, so
//     repeating a call at the same instant is idempotent with respect to
//     refill.
//   - n == 0 consumes nothing and is always allowed. n < 0 is an argument
//     error (ErrNegativeN), a different class from "not enough tokens".
//     n greater than the bucket capacity fails immediately with
//     ErrExceedsCapacity because it can never be satisfied.
//   - When a request is rejected for lack of tokens, Decision.Wait reports
//     how long to wait until the bucket holds enough, rounded up to the
//     nanosecond (within one minimal time unit of the true value). If the
//     rate is zero the wait can never elapse and Wait is math.MaxInt64
//     nanoseconds.
//
// Tenants are fully isolated. The limiter shards its buckets (64 shards,
// one mutex each) so concurrent calls for different tenants do not
// contend on a single global lock, and concurrent calls for one tenant
// can never hand out more tokens than the bucket holds plus what refilled
// meanwhile. Idle tenants can be reclaimed with ReclaimIdle; a reclaimed
// tenant that reappears starts again with a full bucket.
package ontology
