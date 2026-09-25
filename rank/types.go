package rank

// Direction 表示分区内按排序值 Value 的排序方向。
type Direction int

const (
	Asc  Direction = 1  // 升序
	Desc Direction = -1 // 降序
)

// Row 是参与排名的一行输入。Partition 为 nil 时该行被拒绝。
type Row struct {
	Partition *string
	Value     float64
	ID        string
}

// Ranking 是某一行的三种排名结果。
type Ranking struct {
	ID        string
	Partition string
	Value     float64
	RowNumber int
	Rank      int
	DenseRank int
}

// Stats 汇总一次排名调用的可读出统计。
type Stats struct {
	SkippedNilPartition int
	SkippedNaN          int
	Comparisons         int64
}

// Result 是一次排名调用的完整返回。
type Result struct {
	Rankings []Ranking
	Stats    Stats
}
