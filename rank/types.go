// Package rank computes SQL-style window rankings (ROW_NUMBER, RANK,
// DENSE_RANK) over in-memory rows, partitioned by a partition key.
package rank

// Row is one input record. Partition must be non-nil; a nil Partition
// causes the row to be rejected. Value is the float64 sort key; NaN
// values are rejected. ID is a stable row identifier used to order
// rows that tie on Value.
type Row struct {
	Partition *string
	Value     float64
	ID        string
}

// RankedRow is a Row together with its three ranking columns.
// All three are 1-based within the row's partition.
type RankedRow struct {
	Row       Row
	RowNumber int
	Rank      int
	DenseRank int
}

// Config controls ranking behavior.
type Config struct {
	// Descending reverses the value ordering. Ties are still broken by
	// ascending row ID, never by descending ID.
	Descending bool
	// Compare optionally overrides value comparison. It must return a
	// negative number, zero, or a positive number when a sorts before,
	// equal to, or after b. When nil, rows compare by Value ascending.
	// Ties (zero) are always broken by ascending row ID.
	Compare func(a, b Row) int
}

// Result is the output of Rank.
type Result struct {
	// Rows holds one RankedRow per accepted input row, ordered by
	// partition key lexicographically, then by rank order within each
	// partition. It is always newly allocated.
	Rows []RankedRow
	// Skipped is the total number of rejected input rows.
	Skipped int
	// SkippedNilPartition counts rows rejected for a nil Partition.
	SkippedNilPartition int
	// SkippedNaN counts rows rejected for a NaN Value.
	SkippedNaN int
}
