// Package rank computes SQL-window-style ranking functions
// (ROW_NUMBER, RANK, DENSE_RANK) over in-memory rows, per partition.
package rank

// Row is one input record.
//
// Partition is a pointer so that a missing partition key (nil) can be
// distinguished from an explicitly empty key (""), which is a legal
// partition. Rows with a nil Partition are rejected and counted as
// skipped. Value is the float64 the ranking sorts by; NaN values are
// rejected and counted as skipped. ID is a stable row identifier used
// to order rows that tie on Value.
type Row struct {
	Partition *string
	Value     float64
	ID        string
}

// RankedRow is a Row together with its three ranking values, all
// 1-based within its partition.
type RankedRow struct {
	Row       Row
	RowNumber int
	Rank      int
	DenseRank int
}

// PartitionResult holds the ranked rows of one partition, ordered by
// sort value (and row ID ascending within ties).
type PartitionResult struct {
	Key  string
	Rows []RankedRow
}

// Result is the outcome of one Compute call.
type Result struct {
	// Partitions is ordered by partition key in lexicographic order.
	Partitions []PartitionResult
	// Skipped is the total number of rejected rows.
	Skipped int
	// SkippedNilPartition counts rows rejected for a nil partition key.
	SkippedNilPartition int
	// SkippedNaN counts rows rejected for a NaN sort value.
	SkippedNaN int
}

// Options configures a Compute call.
type Options struct {
	// Descending sorts larger values first. Tie handling is unchanged:
	// rows with equal values are still ordered by row ID ascending, and
	// RANK/DENSE_RANK share the same tie semantics as ascending order.
	Descending bool
	// CompareValues compares two sort values, returning a negative
	// number, zero, or a positive number. It is used both for ordering
	// and for deciding ties (zero means tied). If nil, the natural
	// float64 ordering is used, in which +0.0 equals -0.0 and +/-Inf
	// are legal endpoints. It is never called with NaN.
	CompareValues func(a, b float64) int
}
