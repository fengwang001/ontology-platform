package ontology

type Direction int

const (
	Ascending Direction = iota
	Descending
)

type InputRow struct {
	PartitionKey *string
	SortValue    float64
	RowID        string
}

type RankedRow struct {
	PartitionKey string
	SortValue    float64
	RowID        string
	RowNumber    int
	Rank         int
	DenseRank    int
}

type SkipCounts struct {
	MissingPartitionKey int
	NaNSortValue        int
	Total               int
}

type RankResult struct {
	Rows         []RankedRow
	Skipped      SkipCounts
	Comparisons  int
}

type RankingOptions struct {
	Direction Direction
}
