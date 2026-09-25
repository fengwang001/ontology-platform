// Package rank computes SQL-window-style ranking functions
// (ROW_NUMBER, RANK, DENSE_RANK) over in-memory rows, partitioned
// by a partition key. It holds no state between calls.
package rank

// Row is a single input record.
//
// Partition selects the partition the row belongs to. A nil Partition
// is invalid: the row is rejected and counted in Result.SkippedNilPartition.
// A non-nil pointer to the empty string is a valid partition key.
type Row struct {
	Partition *string
	Value     float64
	ID        int64
}

// RankedRow is a row together with its three ranking values.
// All three are 1-based within the row's partition.
type RankedRow struct {
	Row       Row
	RowNumber int
	Rank      int
	DenseRank int
}

// Options configures a Compute call.
type Options struct {
	// Desc sorts rows by Value descending instead of ascending.
	// Ties are still broken by ID ascending, never descending.
	Desc bool

	// OnCompare, if non-nil, is invoked once per row comparison
	// performed while sorting. It exists so callers (and tests)
	// can bound the comparison count. It must be cheap.
	OnCompare func()
}

// Result is the outcome of a Compute call.
type Result struct {
	// Rows contains every accepted row, ordered by partition key
	// lexicographically, then by Value (ascending, or descending
	// when Options.Desc is set), with ties broken by ID ascending.
	Rows []RankedRow

	// SkippedNilPartition counts rows rejected for a nil Partition.
	SkippedNilPartition int

	// SkippedNaN counts rows rejected for a NaN Value.
	SkippedNaN int
}
