// Package ranking 提供对标 SQL 窗口函数 ROW_NUMBER / RANK / DENSE_RANK
// 的进程内排名计算。只做排名，不涉及存储、查询解析与聚合框架。
package ranking

// Row 是参与排名的一行数据。
//
// PartitionKey 为分区键：nil 表示缺失（该行会被拒绝并计入跳过计数），
// 指向空串是合法分区。SortValue 为排序值，NaN 会被拒绝，+0.0 与 -0.0
// 视为相等，±Inf 合法。ID 是稳定的行标识，用于并列时确定次序。
type Row struct {
	PartitionKey *string
	SortValue    float64
	ID           string
}

// RankedRow 是一行及其三种排名结果。三种排名均从 1 开始。
type RankedRow struct {
	Row       Row
	RowNumber int // 并列内部按行 ID 升序决定次序
	Rank      int // 并列同名次，并列后跳号
	DenseRank int // 并列同名次，并列后不跳号
}

// Result 是一次排名计算的完整输出。
type Result struct {
	// Rows 按分区键字典序排列分区，分区内按排名次序排列。
	Rows []RankedRow
	// SkippedNilPartition 是因分区键缺失（nil）被跳过的行数。
	SkippedNilPartition int
	// SkippedNaN 是因排序值为 NaN 被跳过的行数。
	SkippedNaN int
}

// Options 控制排名行为，零值表示升序、使用默认数值比较。
type Options struct {
	// Descending 为 true 时按排序值降序排名；并列内部仍按行 ID 升序。
	Descending bool
	// Compare 比较两个排序值，返回负数/零/正数。为 nil 时使用默认
	// 数值比较（+0.0 与 -0.0 相等）。注入计数比较器可统计比较次数。
	Compare func(a, b float64) int
}

// StringPtr 返回指向 s 的指针，便于构造分区键。
func StringPtr(s string) *string { return &s }
