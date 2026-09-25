package rank

// Direction controls whether larger or smaller SortValue ranks first.
type Direction int

const (
	// Asc ranks smaller values first.
	Asc Direction = iota
	// Desc ranks larger values first.
	Desc
)

// Row is one input record. Partition is a pointer so that a missing
// partition key (nil) is distinguishable from the legal empty-string
// partition "".
type Row struct {
	Partition *string
	ID        string
	SortValue float64
}

// RankedRow carries the three window-function ranks for one input row.
type RankedRow struct {
	Partition string
	ID        string
	SortValue float64
	RowNumber int
	Rank      int
	DenseRank int
}

// Result is the full outcome of one Rank call.
type Result struct {
	// Rows is freshly allocated, ordered by partition key (lexicographic),
	// then by the ordering rules of the requested direction and by ID
	// ascending inside ties.
	Rows []RankedRow
	// SkippedNilPartition counts rows whose Partition pointer was nil.
	SkippedNilPartition int
	// SkippedNaN counts rows whose SortValue was NaN.
	SkippedNaN int
	// Comparisons counts ordering comparisons performed while sorting,
	// summed over all partitions.
	Comparisons int64
}

// Skipped is the total number of rejected rows.
func (r Result) Skipped() int {
	return r.SkippedNilPartition + r.SkippedNaN
}
