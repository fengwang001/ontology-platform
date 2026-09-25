// Package ontology computes SQL-window-style ranking functions
// (ROW_NUMBER, RANK, DENSE_RANK) over in-memory rows.
package ontology

// Row is one input record. Partition may be nil, which marks the row
// as invalid; such rows are rejected and counted as skipped.
type Row struct {
	Partition *string
	Value     float64
	ID        string
}

// RankedRow is one output record with all three ranking columns.
type RankedRow struct {
	Row       Row
	Partition string
	RowNumber int
	Rank      int
	DenseRank int
}

// Options controls ranking behavior.
type Options struct {
	// Descending sorts values high-to-low. Tie-breaking by ID stays
	// ascending regardless of this flag.
	Descending bool
	// Compare, when non-nil, replaces the default row comparator used
	// for sorting. It must order rows by value (honoring Descending)
	// and break ties by ID ascending. It is useful for instrumentation
	// such as counting comparisons.
	Compare func(a, b Row) int
}

// Summary is the result of a Compute call.
type Summary struct {
	// Rows holds ranked rows ordered by partition key lexicographically,
	// then by ranking order within each partition.
	Rows []RankedRow
	// SkippedNilPartition counts rows rejected for a nil partition key.
	SkippedNilPartition int
	// SkippedNaN counts rows rejected for a NaN sort value.
	SkippedNaN int
}

// StringPtr is a small helper for building partition keys in tests
// and demos.
func StringPtr(s string) *string { return &s }
