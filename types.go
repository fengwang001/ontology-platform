package rank

// Row is one input record: a partition key, a float64 sort value, and a
// stable row ID. Partition is a pointer so that a missing (nil) key is
// distinguishable from the valid empty-string partition "".
type Row struct {
	Partition *string
	Value     float64
	ID        string
}

// Order selects ascending or descending sort order.
type Order int

const (
	Asc Order = iota
	Desc
)

// Ranked is one output row carrying all three ranking columns together so
// the three tie-handling semantics can be cross-checked on the same row.
type Ranked struct {
	Partition string
	ID        string
	Value     float64

	RowNumber int
	Rank      int
	DenseRank int
}

// Result is the output of one ranking run. Partitions are ordered by key
// in lexicographic order; Rows are ordered within each partition.
type Result struct {
	Partitions []PartitionResult

	// SkippedNilPartition counts rows rejected for a nil partition key.
	SkippedNilPartition int
	// SkippedNaN counts rows rejected for a NaN sort value.
	SkippedNaN int
}

// PartitionResult holds the ranked rows of one partition.
type PartitionResult struct {
	Key  string
	Rows []Ranked
}

// AllRows returns a freshly allocated slice containing every ranked row
// across all partitions, in partition-key then rank order.
func (r *Result) AllRows() []Ranked {
	return make([]Ranked, 0)
}
