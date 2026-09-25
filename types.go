package ontology

// Direction controls whether ordering values rank ascending or descending.
type Direction int

const (
	Asc Direction = iota
	Desc
)

// Row is one input row. PartitionKey is a pointer so that a missing
// partition (nil) is distinguishable from the legal empty-string partition.
type Row struct {
	PartitionKey *string
	Value        float64
	ID           string
}

// Result carries the three rank columns for one input row, identified by ID.
type Result struct {
	PartitionKey string
	ID           string
	Value        float64
	RowNumber    int
	Rank         int
	DenseRank    int
}

// Stats reports rejected rows and comparator work.
type Stats struct {
	SkippedNilPartition int
	SkippedNaN          int
	Comparisons         int
}

// Options configures a Rank call.
type Options struct {
	Order Direction
	// CountComparisons, when non-nil, is incremented once per value/ID
	// comparison performed while ordering rows within a partition.
	CountComparisons *int
}
