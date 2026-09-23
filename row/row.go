// Package row 定义分页数据集的行与复合排序键。
//
// 排序键为 (Score 升序, ID 升序) 的全序：先比 float64 排序值，
// 相等再比唯一字符串 ID，从而在排序值重复时仍能唯一定位一行。
package row

import "sort"

// Row 是数据集中的一行：一个 float64 排序值加一个唯一字符串 ID。
type Row struct {
	Score float64
	ID    string
}

// Less 报告复合键 a 是否严格小于 b：先比 Score，相等再比 ID。
func Less(a, b Row) bool {
	if a.Score != b.Score {
		return a.Score < b.Score
	}
	return a.ID < b.ID
}

// Greater 报告复合键 a 是否严格大于 b。
func Greater(a, b Row) bool {
	return Less(b, a)
}

// Equal 报告两行复合键是否完全相同。
func Equal(a, b Row) bool {
	return a.Score == b.Score && a.ID == b.ID
}

// Key 提取一行的复合键（这里行本身即键值载体）。
func Key(r Row) Row { return r }

// Sort 按 (Score, ID) 全序对切片原地排序。
func Sort(rows []Row) {
	sort.Slice(rows, func(i, j int) bool { return Less(rows[i], rows[j]) })
}
