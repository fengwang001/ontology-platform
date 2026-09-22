package ontology

// Direction controls whether ordering values are read ascending or descending.
type Direction int

const (
	Ascending Direction = iota
	Descending
)

// Row is one input record. PartitionKey is a pointer so that a nil key
// (missing partition) is distinguishable from the legal empty-string key.
type Row struct {
	PartitionKey *string
	OrderValue   float64
	ID           string
}

// ResultRow carries the three ranks for one input row.
type ResultRow struct {
	PartitionKey string
	OrderValue   float64
	ID           string
	RowNumber    int
	Rank         int
	DenseRank    int
}

// Result is the ranking output: new result rows and rejection statistics.
type Result struct {
	Rows          []ResultRow
	SkippedNilKey  int
	SkippedNaN     int
	SkippedTotal   int
	CompareCounts  map[string]int
}
