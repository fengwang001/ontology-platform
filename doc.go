// Package ontology provides a pluggable sort-key generator for keeping
// an ordered sequence of elements without rewriting existing keys.
//
// Each element carries a string key; the byte-wise order of the keys is
// the order of the elements. A Generator produces a new key strictly
// between two neighbor keys, so elements can be inserted anywhere —
// front, back, or between two adjacent elements — without touching any
// other key.
//
// Key rules:
//   - Keys use a fixed ordered charset: "0123456789abcdefghijklmnopqrstuvwxyz".
//   - Generated keys never end with the smallest character ('0'), so a
//     gap always remains between any two generated keys.
//   - Key length is capped; exceeding the cap fails with
//     ErrNeedsRebalance instead of silently growing or truncating.
//
// Sequence adds concurrency-safe inserts with deterministic conflict
// resolution, atomic rebalancing, snapshots, and a SelfCheck.
package ontology
