// Package rank computes SQL-window-style ranking functions
// (ROW_NUMBER, RANK, DENSE_RANK) over in-memory rows, per partition.
package rank

// Row is one input record. Partition is a pointer so that a missing
// partition key (nil) can be distinguished from the empty string,
// which is a valid partition.
type Row struct {
	Partition *string
	Value     float64
	ID        int64
}

// Ranked is one output record: the input row plus its three rankings
// within its partition.
type Ranked struct {
	Row       Row
	Partition string
	RowNumber int
	Rank      int
	DenseRank int
}

// Options controls ranking behavior.
type Options struct {
	// Descending sorts larger values first. Ties are still ordered by
	// ascending row ID.
	Descending bool
	// Less, when non-nil, replaces the default ordering comparator.
	// It is the only comparison used while sorting, so tests can wrap
	// it to count comparisons. It must establish the same total order
	// as the default comparator for the results to be meaningful.
	Less func(a, b Row) bool
}

// Summary is the result of a Compute call.
type Summary struct {
	// Results holds one Ranked per accepted row, ordered by partition
	// key lexicographically, then by rank order within the partition.
	Results []Ranked
	// Skipped counts rejected rows (nil partition key or NaN value).
	Skipped int
}

// Str returns a pointer to s, for building Row literals.
func Str(s string) *string { return &s }
