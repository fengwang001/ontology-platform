package ontology

// Row is one input record: a partition key, a float64 sort value, and a
// stable row identifier.
type Row struct {
	Partition *string
	Value     float64
	ID        string
}

// Ranking carries all three window-function ranks for a single row.
type Ranking struct {
	Partition string
	ID        string
	Value     float64
	RowNumber int
	Rank      int
	DenseRank int
}

// Result groups the freshly allocated ranking output with counters for rows
// that were rejected before ranking.
type Result struct {
	Rankings        []Ranking
	SkippedNilPart  int
	SkippedNaN      int
	ComparisonCount int64
}
