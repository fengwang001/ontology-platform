package ontology

// Row is one input record for ranking.
//
// Partition is a required partition key: the empty string is a legal key,
// but a nil partition makes the row invalid (see RankResult.Skipped).
// Value is the sort key. ID uniquely and stably identifies a row; ties on
// Value are ordered by ascending ID.
type Row struct {
	Partition *string
	Value     float64
	ID        string
}

// RankedRow carries the three ranking columns for one input row.
//
// RowNumber counts every row starting at 1 (1,2,3,...).
// Rank leaves gaps after ties (1,2,2,4,...).
// DenseRank never leaves gaps (1,2,2,3,...).
type RankedRow struct {
	Partition string
	Value     float64
	ID        string
	RowNumber int
	Rank      int
	DenseRank int
}

// RankResult is the output of one ranking pass.
type RankResult struct {
	// Rows is freshly allocated; it never aliases the input slice or its rows.
	// Partitions are ordered lexicographically by key; rows inside a
	// partition are ordered by (Value, ID) per the requested direction.
	Rows []RankedRow

	// Skipped counts rejected rows: nil partition key or NaN sort value.
	Skipped int
}

// Direction controls the ordering of sort values within a partition.
type Direction int

const (
	// Ascending orders values from small to large.
	Ascending Direction = iota
	// Descending orders values from large to small; ties still break by
	// ascending ID, and tie semantics are unchanged.
	Descending
)

// Rank computes ROW_NUMBER / RANK / DENSE_RANK independently per partition.
//
// The input slice and every input Row are left untouched. Rows with a nil
// partition key or a NaN Value are rejected and counted in Skipped.
func Rank(rows []Row, dir Direction) RankResult {
	return rankWithLess(rows, dir, nil)
}
