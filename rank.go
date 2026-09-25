package ontology

// Order controls how ranking values are ordered inside a partition.
type Order int

const (
	// Asc ranks smaller values ahead of larger values.
	Asc Order = iota
	// Desc ranks larger values ahead of smaller values.
	Desc
)

// InputRow is one row supplied to RankRows.
//
// A nil Partition is invalid and the row is skipped. Value must be a
// finite or infinite number; NaN is invalid. ID must be stable and is
// used, in ascending order, to break ties.
type InputRow struct {
	Partition *string
	Value     float64
	ID        string
}

// Options configures a ranking run.
type Options struct {
	Order Order
}

// Stats reports how many input rows were rejected and why.
type Stats struct {
	SkippedNilPartition int
	SkippedNaN          int
}

// RankedRow carries the three ranking columns for one input row.
//
// RowNumber numbers every row 1..n (ties broken by ID ascending). Rank
// leaves gaps after ties. DenseRank never leaves gaps.
type RankedRow struct {
	Partition string
	Value     float64
	ID        string
	RowNumber int
	Rank      int
	DenseRank int
}

// Result is the outcome of a ranking run.
type Result struct {
	Rows  []RankedRow
	Stats Stats
}
