// Package ontology implements a bounded-memory, per-group Top-N selector.
//
// Rows (map[string]any) are streamed in via Add. Each row is assigned to a
// group by a configured group-key column, and within each group only the N
// best rows (by a numeric score column) are retained. The number of rows
// held in memory at any moment never exceeds groups x N; it is independent
// of the total number of rows processed.
//
// Ordering inside a group (best first):
//
//  1. score: descending (higher is better); +Inf/-Inf are valid scores.
//  2. tie column: the configured string column, ascending lexicographic
//     order; rows where the column is missing or not a string use "".
//  3. content identity: a deterministic identifier derived from the row's
//     own content (never an arrival index). Keys are sorted, each pair is
//     encoded as "key\x00<typetag>:<value>\x00", the concatenation is hashed
//     with FNV-1a 64; rows compare by hash, then by the raw canonical
//     encoding. Because the identity depends only on content, shuffling the
//     input yields element-wise identical Top-N results.
//
// Group keys: a missing group-key column, an explicit nil value, and an
// empty string are three DISTINCT groups. Snapshot orders groups by kind
// (Missing < Null < Value) and then by the key's string form ascending.
//
// Rows whose score column is missing or non-numeric do not participate in
// ranking but are counted per group (SkippedNonNumeric). Rows with a NaN
// score are likewise excluded and counted separately (SkippedNaN).
//
// Selector is safe for concurrent use.
package ontology
