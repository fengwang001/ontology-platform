// Package ontology contains a deterministic multi-tenant token bucket limiter.
//
// Time is always supplied by the caller; this package never reads the wall
// clock. Refill arithmetic uses nanosecond fixed-point values: elapsed
// nanoseconds are multiplied by the per-second rate, so fractional tokens are
// retained across calls instead of being rounded away.
//
// If the supplied time equals the previous time, elapsed time is zero and no
// additional token is refilled. If it is earlier, Allow and AvailableTokens
// return ErrClockMovedBack and leave the bucket unchanged.
//
// A zero-token request is allowed without validation against bucket time or
// capacity. Negative requests return ErrNegativeTokens. A request larger than
// capacity can never succeed and returns ErrRequestExceedsCapacity. A normal
// shortage returns an error wrapping ErrTokensUnavailable; use AsDenied to get
// the whole-token floor and exact ceiling wait.
//
// Buckets are sharded. Requests for tenants in different shards proceed under
// independent read locks; each tenant's state is additionally protected by its
// own mutex. ReapInactive removes only buckets whose last activity is strictly
// before cutoff; a later AddTenant starts with a full bucket.
package ontology
