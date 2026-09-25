// Package ontology 在本工程中仅实现 SQL 风格的窗口排名函数
// （ROW_NUMBER / RANK / DENSE_RANK）。
//
// 排名完全在进程内存中完成，只依赖标准库；不涉及存储、查询解析、
// 聚合框架、Link、Action 或 HTTP。
package ontology

// Row 是参与排名的一行输入。
//
// PartitionKey 为分区键指针：nil 表示分区键缺失，该行会被拒绝；
// 指向空串是合法分区。SortValue 为排序值，NaN 会被拒绝。
// ID 是稳定行 ID，用于并列内部的确定性次序（ID 升序）。
type Row struct {
	PartitionKey *string
	SortValue    float64
	ID           string
}

// Order 指定排序值的方向。
type Order int

const (
	// Ascending 表示排序值升序（小值在前）。
	Ascending Order = iota
	// Descending 表示排序值降序（大值在前）；并列内部仍按 ID 升序。
	Descending
)

// Result 是一行输入对应的三列排名结果。
type Result struct {
	PartitionKey string
	ID           string
	SortValue    float64

	// RowNumber 在分区内行号，从 1 开始，并列行之间按 ID 升序拉开。
	RowNumber int
	// Rank 为 RANK()：并列同名次，并列之后跳号。
	Rank int
	// DenseRank 为 DENSE_RANK()：并列同名次，并列之后不跳号。
	DenseRank int
}

// Stats 汇报一次排名调用的可观测统计。
type Stats struct {
	// SkippedNilPartition 是因分区键为 nil 被跳过的行数。
	SkippedNilPartition int64
	// SkippedNaN 是因排序值为 NaN 被跳过的行数。
	SkippedNaN int64
	// SkippedTotal 是被跳过的总行数（每个坏行恰好计一次）。
	SkippedTotal int64
	// Comparisons 是排序过程中对排序值/行 ID 的比较总次数。
	Comparisons int64
}

// Options 控制一次排名调用的行为。
type Options struct {
	// Order 为排序值方向；零值 Ascending。
	Order Order
}
