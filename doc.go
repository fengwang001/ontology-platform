// Package hll overview.
//
// # Register semantics
//
// An estimator holds m = 2^p 8-bit registers (p in [4,16]). For every added
// 64-bit hash h:
//
//   - register index  j = h mod 2^p         (the low p bits)
//   - rank            w = rho(h >> p)        (leading zeros of the remaining
//     64-p high bits, plus one)
//   - update rule     M[j] = max(M[j], w)
//
// rho ranges in [1, 64-p+1]; the upper bound is reached when all 64-p high
// bits are zero (the convention treats the rank as "a 1 bit just past the
// end"). Registers are write-maximum only: a later, smaller rho never
// overwrites a larger one. InspectRegisters exposes the exact register array
// as a defensive copy for testing.
//
// # Exact small cardinality
//
// While fewer than 256 distinct hashes have been seen the estimator
// additionally stores the distinct hashes in a set, so Estimate returns the
// exact distinct count (0 elements => 0). After that it discards the set and
// Estimate uses the standard HyperLogLog harmonic-mean formula with linear
// counting correction for the remaining-empty-registers regime. Register
// updates happen in both modes and are always available via
// InspectRegisters.
//
// # Merge
//
// Merge combines independent sketches register-wise with max, which
// corresponds to set union. Merge never mutates its inputs and requires both
// estimators to use the same p; mismatched p returns a
// PrecisionMismatchError (wrapping ErrPrecisionMismatch) carrying both
// values.
//
// # Supported operations
//
//   - New(p): construct a validated estimator
//   - Add(hash): observe one precomputed 64-bit hash (idempotent)
//   - Estimate(): distinct-hash count, exact below the sparse threshold
//   - Merge(a, b): union two estimators
//   - InspectRegisters(): test-only snapshot of the register array
//
// # Deliberately unsupported operations
//
// Remove/Delete is intentionally NOT provided. HyperLogLog registers only
// record the maximum observed rho per bucket; there is no reference
// counting or per-element record in dense mode, so deleting one element
// cannot tell whether its contribution is still "needed" (another distinct
// element may share the same register and rank). Any naive decrement would
// silently corrupt the estimate. Callers needing removals must use a
// different data structure (e.g. an exact set, a linear-probabilistic
// counter with per-item bookkeeping, or sketch-window rotation).
//
// Duplicate Adds are harmless by construction: max(rho,rho)=rho, and in
// sparse mode the hash set collapses repeats, so adding the same hash any
// number of times never changes registers, snapshot, or Estimate.
package hll
