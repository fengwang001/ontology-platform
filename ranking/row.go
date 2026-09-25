// Package ranking 提供对标 SQL 窗口函数 ROW_NUMBER / RANK / DENSE_RANK
// 的进程内排名计算。所有状态仅存在于单次调用的内存中。
package ranking

// Row 是参与排名的一行数据。
type Row struct {
	// Partition 是分区键；为 nil 时该行被拒绝并计入跳过计数。
	// 空字符串是合法分区键。
	Partition *string
	// Value 是排序值；NaN 行被拒绝，+0.0 与 -0.0 视为相等，±Inf 合法。
	Value float64
	// ID 是稳定的行标识，用于并列时确定 ROW_NUMBER 的次序（升序）。
	ID string
}

// RankedRow 是一行的排名结果，三种排名同时给出。
type RankedRow struct {
	Row       Row
	RowNumber int // 从 1 开始，并列内部按行 ID 升序
	Rank      int // 并列同名次，并列后跳号
	DenseRank int // 并列同名次，并列后不跳号
}

// Result 是一次排名调用的完整输出。
type Result struct {
	// Rows 按分区键字典序排列，分区内按排序值有序、并列按行 ID 升序。
	Rows []RankedRow
	// Skipped 是被拒绝（nil 分区键或 NaN 排序值）的行数。
	Skipped int
}

// Options 控制排名行为。
type Options struct {
	// Descending 为 true 时按排序值降序排名；并列内部仍按行 ID 升序。
	Descending bool
	// Compare 比较两行，返回负数/零/正数。为 nil 时使用默认比较器
	// （先按排序值，再按行 ID 升序）。测试可注入计数比较器。
	Compare func(a, b Row) int
}
