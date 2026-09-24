// Package row 定义分页数据集的行及其复合排序键。
//
// 复合键为 (Score float64, ID string)：先比 Score，Score 相等再按
// 字符串字节序比 ID。ID 全局唯一，因此复合键构成全序。
package row

import "math"

// Row 是数据集中的一行：一个排序值加一个唯一字符串 ID。
type Row struct {
	Score float64
	ID    string
}

// New 构造一行。
func New(score float64, id string) Row { return Row{Score: score, ID: id} }

// Valid 报告行是否可进入有序数据集。NaN 无法参与全序比较，
// 空 ID 无法保证唯一性，两者都拒绝。
func Valid(r Row) bool {
	return !math.IsNaN(r.Score) && r.ID != ""
}

// Compare 按复合键字典序比较两行：
// a < b 返回 -1，a == b 返回 0，a > b 返回 1。
func Compare(a, b Row) int {
	switch {
	case a.Score < b.Score:
		return -1
	case a.Score > b.Score:
		return 1
	}
	switch {
	case a.ID < b.ID:
		return -1
	case a.ID > b.ID:
		return 1
	}
	return 0
}

// Less 报告 a 在全序中是否严格排在 b 之前。
func Less(a, b Row) bool { return Compare(a, b) < 0 }

// Key 是 Row 的别名，强调游标记录的是复合排序键位置。
type Key = Row
