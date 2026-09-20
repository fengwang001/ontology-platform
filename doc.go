// Package ontology implements a two-table equi-joiner over in-memory rows.
//
// Each table is a []map[string]any. Rows are joined on an ordered list of
// key columns with SQL three-valued NULL semantics. Only Inner and Left
// modes are supported. All state lives in process memory; the package uses
// only the standard library.
//
// # NULL key semantics
//
// A join key is "empty" for a row when any key column is absent, is nil, or
// is a float64 NaN. Empty keys never compare equal, not even to another
// empty key (NULL is not reflexive). In Inner mode such rows never match.
// In Left mode they are emitted as unmatched rows. Result.LeftNullKeyCount
// counts left rows unmatched because of an empty key, separately from
// Result.LeftUnmatchedCount, which counts left rows that have a full key
// but no matching right row.
//
// # Key comparability
//
// Supported key value types are string, bool, int64 and float64. int64 and
// float64 are mutually comparable and compare by exact numeric value, so
// int64(3) equals float64(3.0). +0.0 and -0.0 are equal. NaN is never
// equal (treated as an empty key, see above). If the same key column has
// incomparable types on the two sides (e.g. string vs int64), Join returns
// a *KeyTypeError naming the column and both types.
//
// # Duplicate keys
//
// Duplicate keys expand fully: m left rows and n right rows on the same key
// produce exactly m*n output rows, each pair exactly once.
// Result.MaxFanout reports the largest per-key m*n.
//
// # Deterministic output order
//
// Output is independent of input row order and map iteration order. Rows
// are sorted by join key columns, column by column, ascending (numeric by
// value, string bytewise, bool false<true). Within one key, matched pairs
// sort by (left row identity, right row identity). Unmatched left rows
// (Left mode) sort after the matched pairs of their key; left rows with an
// empty key sort after all keyed rows, ordered by row identity.
//
// Row identity is derived from row content, never from input position: it
// is the canonical serialization of the row — columns sorted by name, each
// rendered as "name=type:value" with values rendered by exact numeric
// value — see rowID. Two rows with identical content have identical
// identity and are interchangeable, so the output sequence is still
// uniquely determined.
//
// # Result row construction
//
// Every output row is a fresh map. Left columns keep their names. Right
// join-key columns are omitted (they equal the left key). A right non-key
// column that collides with a left column name is renamed to
// "right.<name>"; other right columns keep their names. Result.RightColumns
// lists the right-side column names as they appear in output rows, in
// sorted order. For an unmatched left row in Left mode no right column is
// present at all: right values are absent from the map, not zero values;
// use IsMissing (or a plain map lookup) to detect them.
//
// Output rows are deep copies: mutating an output row never affects an
// input row, and vice versa.
package ontology
