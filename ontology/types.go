// Package ontology 实现本体服务平台的最小子集。
//
// 本文件仅提供窗口排名函数（ROW_NUMBER / RANK / DENSE_RANK）
// 所需的类型定义。
package ontology

import "math"

// Direction 决定分区内按排序值升序还是降序排名。
type Direction int

const (
	// Asc 表示按排序值升序（小值排名靠前）。
	Asc Direction = iota
	// Desc 表示按排序值降序（大值排名靠前）。
	Desc
)

// Row 是参与排名的一行输入。

// Key 为分区键指针：nil 表示“分区键缺失”，该行会被拒绝；
// 指向空串是合法分区。Value 为排序值；NaN 会被拒绝。
// ID 是行的稳定标识，并列时按 ID 升序决定 ROW_NUMBER。
type Row struct {
	Key   *string
	Value float64
	ID    string
}

// RankedRow 是一行的排名结果，三列排名同时返回以便互相核对。
type RankedRow struct {
	Key       string
	ID        string
	Value     float64
	RowNumber int64
	Rank      int64
	DenseRank int64
}

// Stats 汇总一次 Rank 调用中可读出的跳过计数与比较次数。
type Stats struct {
	// SkippedNilKey 是分区键为 nil 而被拒绝的行数。
	SkippedNilKey int
	// SkippedNaN 是排序值为 NaN 而被拒绝的行数。
	SkippedNaN int
	// Comparisons 是排序过程中实际发生的值比较总次数。
	Comparisons int64
}

// Order 为排名调用提供选项。零值即为合法（升序、无计数比较器）。
type Order struct {
	Direction Direction
	// Comparator 非 nil 时，每次值比较都会计入该计数器，
	// 用于测试比较次数上界。
	Comparator *CountingComparator
}

// isNaN 判定排序值是否为 NaN（单独包装便于阅读与测试）。
func isNaN(v float64) bool {
	return math.IsNaN(v)
}
