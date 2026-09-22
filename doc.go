// Package ontology implements a HyperLogLog cardinality estimator.
//
// Elements arrive as 64-bit hash values (the caller hashes; the estimator
// only accepts uint64). At any time the estimator can approximate how many
// distinct hashes have been seen, and multiple estimators can be merged.
// All state lives in process memory; only the standard library is used.
//
// Register semantics (every bit is auditable):
//   - p is configurable in [4, 16] and validated at construction time.
//   - The low p bits of the hash select one of the 1<<p registers.
//   - rho is the number of leading zeros of the remaining high bits, plus
//     one. When all remaining bits are zero, rho = 64 - p + 1.
//   - A register is overwritten only when the new rho exceeds its old value.
//
// InspectRegisters returns a copy of the register snapshot for testing;
// mutating the copy never affects internal state.
//
// Deliberately not provided:
//   - Remove: HyperLogLog registers only keep a maximum, so a deletion
//     cannot know which insertion set the current value and cannot be
//     undone. Use a different sketch (e.g. a counting Bloom filter) if
//     deletions are required.
//   - Storage, aggregation frameworks, query parsing, Link/Action, HTTP.
//
// Add is idempotent: adding the same hash any number of times never
// changes the estimate.
package ontology
