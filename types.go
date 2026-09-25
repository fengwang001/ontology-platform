// Package ontology provides in-memory ranking functions matching the
// semantics of SQL window functions ROW_NUMBER, RANK and DENSE_RANK.
package ontology

// Row is a single input record to be ranked.
//
// Partition selects the ranking group; a nil Partition is invalid and the
// row is skipped. Value is the float64 sort key; NaN values are invalid and
// the row is skipped. ID is a stable identifier used to deterministically
// order rows that tie on Value.
type Row struct {
	Partition *string
	Value     float64
	ID        string
}

// RankedRow is a Row together with its three ranking columns, computed
// within its partition.
type RankedRow struct {
	Row Row
	// RowNumber is the 1-based position of the row in its partition,
	// with ties broken by ascending Row.ID (like SQL ROW_NUMBER).
	RowNumber int
	// Rank is the 1-based rank with gaps after ties (like SQL RANK).
	Rank int
	// DenseRank is the 1-based rank without gaps (like SQL DENSE_RANK).
	DenseRank int
}

// Result is the outcome of a Rank call.
type Result struct {
	// Rows holds every ranked row, ordered by partition key
	// lexicographically, then by ranking position within the partition.
	// It is always newly allocated and never aliases the input.
	Rows []RankedRow
	// SkippedNilPartition counts rows rejected because Partition was nil.
	SkippedNilPartition int
	// SkippedNaN counts rows rejected because Value was NaN.
	SkippedNaN int
}

// Skipped returns the total number of rejected rows.
func (r Result) Skipped() int {
	return r.SkippedNilPartition + r.SkippedNaN
}

// Options configures a Rank call.
type Options struct {
	// Descending sorts values from largest to smallest. Tie handling and
	// the ascending-ID tiebreak inside ties are unaffected.
	Descending bool
	// Compare compares two sort values, returning a negative number, zero
	// or a positive number when a orders before, equal to, or after b in
	// ascending order. It must be direction-independent; Descending is
	// applied on top of it. When nil, CompareFloat is used.
	Compare func(a, b float64) int
}
