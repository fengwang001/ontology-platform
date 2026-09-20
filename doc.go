// Package ontology implements a two-table equi-join connector.
//
// The connector joins two in-memory tables (slices of Row) on an ordered
// list of join keys, in either Inner or Left mode. It deliberately follows
// SQL three-valued semantics for NULL keys, expands duplicate keys into the
// full cartesian product per key, and produces a fully deterministic output
// order that is independent of input row order and map iteration order.
//
// # NULL key semantics
//
// A join key value is "empty" (NULL) when the attribute is absent from the
// row, when it is nil, or when it is a float64 NaN. NULL is not a value: it
// never equals anything, not even another NULL. In Inner mode a row with an
// empty key never matches. In Left mode such a left row is still emitted as
// an unmatched row. Stats.LeftNullKey counts left rows that went unmatched
// because of an empty key, separately from Stats.LeftNoPartner, which counts
// left rows whose key had a value but no partner on the right.
//
// # Key types and comparison
//
// Supported key types: string, bool, and numeric (any signed/unsigned int
// kind and float64). int64 and float64 are mutually comparable and compare
// by numeric value; +0.0 and -0.0 are equal. If the same key name is seen
// with incomparable types on the two sides (e.g. string on the left and
// int64 on the right), Join returns a *KeyTypeError naming the key and both
// types. Unsupported key types return a *KeyTypeError as well.
//
// # Output order and row identity
//
// Matched rows are ordered by the join key tuple, column by column,
// ascending. Ties within one key are broken by row identity: the canonical
// serialization of the row's content (attributes sorted by name, each
// rendered as "name=type:value", nested maps and slices rendered
// recursively), first of the left row, then of the right row. Because the
// identity is derived purely from row content, shuffling either input table
// (or ranging over maps in any order) yields a byte-identical result
// sequence. Rows with fully identical content are interchangeable, so ties
// between them cannot affect element-wise equality. Unmatched left rows
// (Left mode only) are appended after all matched rows, ordered by the
// canonical serialization of the left row.
//
// # Result row construction
//
// Every result row is a fresh map holding deep copies of the input values;
// mutating a result never affects an input row and vice versa. Join key
// attributes are taken from the left row. A right-side non-key attribute
// whose name collides with a name already present from the left row is
// emitted under the name "right."+name; the right value never overwrites
// the left value. For unmatched left rows in Left mode no right-side
// attribute is present at all: right attributes are missing from the map
// (detectable with the comma-ok idiom), never zero values.
package ontology
