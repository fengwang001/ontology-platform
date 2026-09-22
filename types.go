package ontology

// Direction controls how SortValue values are ordered within a partition.
type Direction int

const (
	// Asc sorts smaller SortValue first.
	Asc Direction = iota
	// Desc sorts larger SortValue first.
	Desc
)

// Row is one input record. Partition is a pointer so that a missing
// partition key (nil) is distinguishable from the legal empty-string
// partition "".
type Row struct {
	Partition *string
	SortValue float64
	ID        string
}

// RankedRow is one output record. Every input row that is accepted yields
// exactly one RankedRow carrying all three rank numbers simultaneously.
type RankedRow struct {
	Partition string
	SortValue float64
	ID        string
	RowNumber int
	Rank      int
	DenseRank int
}

// Stats reports how many input rows were rejected and why.
type Stats struct {
	// SkippedNilPartition counts rows whose Partition pointer is nil.
	SkippedNilPartition int
	// SkippedNaN counts rows whose SortValue is NaN.
	SkippedNaN int
}

// Result is the full answer for one Rank call.
type Result struct {
	// Rows is freshly allocated and never aliases the input slice.
	Rows  []RankedRow
	Stats Stats
}
