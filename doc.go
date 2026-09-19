// Package ontology implements a concurrency-safe, multi-tenant token
// bucket rate limiter using only the standard library.
//
// # Time is injected
//
// Every method takes the current time as an argument; the limiter never
// calls time.Now. Tests can therefore drive the limiter with a fake clock
// and stay fully deterministic.
//
// # Exact fixed-point refill
//
// Tokens are tracked internally as integers at a scale of 1e9 units per
// token, so a rate of R tokens/second is exactly R units per nanosecond
// and refills accumulate without any truncation or floating-point drift:
// advancing 100ms ten times at 7 tokens/s yields exactly 7 tokens, and
// advancing 1ms a thousand times yields exactly the same amount as a
// single 1s advance. Refill never pushes a bucket above its capacity;
// surplus is dropped.
//
// # Backward and repeated time
//
// If the supplied now is earlier than the bucket's last update, the call
// is treated as if zero time had elapsed: no tokens are added or removed
// by the refill, the bucket's clock is not moved backward, and the call
// is still evaluated against the current state. A now equal to the last
// update is likewise a zero-elapsed no-op for the refill, which makes
// same-instant calls idempotent.
//
// # Request validation
//
// A zero token request always succeeds and consumes nothing. A negative
// request fails with ErrNegativeTokens. A request larger than the bucket
// capacity fails immediately with a *CapacityError (matching
// ErrExceedsCapacity), because it can never be satisfied. Ordinary
// "not enough tokens" is not an error: Allow returns (false, nil) and
// WaitDuration reports the exact wait.
//
// # Isolation, concurrency and reclamation
//
// Tenants are fully isolated and unlimited in number. State is spread
// across 64 lock-striped shards and every bucket has its own mutex, so
// concurrent Allow calls for different tenants never contend on a global
// lock, and concurrent calls for the same tenant never hand out more
// tokens than the bucket holds plus what refilled in the meantime.
// ReclaimIdle drops tenants that have been inactive longer than the
// configured TTL; a reclaimed tenant that returns starts with a full
// bucket, and tenants with calls in flight are never reclaimed.
package ontology
