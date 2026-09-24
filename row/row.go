// Package row 定义分页数据集里的行及其全序排序键。
//
// 复合键 (Score, ID)：先比 Score，Score 相等再按字典序比 ID。
// ID 全局唯一，因此任意两个不同行的复合键都可严格比较。
package row

import "math"

// Row 是数据集中的一行：一个 float64 排序值加一个唯一字符串 ID。
type Row struct {
	Score float64
	ID    string
}

// Less 报告 a 的复合键是否严格小于 b。
// NaN 不参与全序，含 NaN 的比较一律返回 false。
func Less(a, b Row) bool {
	if math.IsNaN(a.Score) || math.IsNaN(b.Score) {
		return false
	}
	if a.Score != b.Score {
		return a.Score < b.Score
	}
	return a.ID < b.ID
}

// Equal 报告两行复合键是否完全相同。
func Equal(a, b Row) bool {
	return a.Score == b.Score && a.ID == b.ID
}

// Compare 返回 -1/0/1，分别表示 a 小于/等于/大于 b。
// 含 NaN 时返回 -2（无全序）。
func Compare(a, b Row) int {
	switch {
	case Equal(a, b):
		return 0
	case Less(a, b):
		return -1
	case Less(b, a):
		return 1
	default:
		return -2
	}
}

// Valid 拒绝 NaN 排序值（NaN 无法纳入全序）。
func Valid(r Row) bool {
	return !math.IsNaN(r.Score)
}
