package ontology

// Order controls the direction in which sort values are ranked inside a
// partition. Ties are always ordered by ascending row ID regardless of order.
type Order int

const (
	// Asc ranks smaller sort values first.
	Asc Order = iota
	// Desc ranks larger sort values first; ties still break by ascending ID.
	Desc
)

// Row is one input record. Partition is a pointer so that a missing (nil)
// partition key can be distinguished from the legal empty-string partition.
type Row struct {
	Partition *string
	Value     float64
	ID        string
}

// RankRow carries the three rankings for one accepted input row.
type RankRow struct {
	ID         string
	Partition  string
	Value      float64
	RowNumber  int
	Rank       int
	DenseRank  int
}

// Report is the full result of one ranking pass.
type Report struct {
	Rows        []RankRow
	Skipped     int
	Comparisons int
}
