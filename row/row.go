// Package row 定义数据集中的行及其排序键。
//
// 排序键是复合键 (Key, ID)：Key 是 float64 排序值，ID 是唯一字符串。
// 因为 ID 唯一，(Key, ID) 构成严格全序，这是游标分页不重不漏的基础。
package row

import "cmp"

// Row 是数据集中的一行。
type Row struct {
	Key float64 // 排序值，允许重复
	ID  string  // 全局唯一标识
}

// Compare 比较两行的复合键：先比 Key，相等再比 ID。
// 返回负数表示 a 排前，0 表示同一行，正数表示 a 排后。
func Compare(a, b Row) int {
	if c := cmp.Compare(a.Key, b.Key); c != 0 {
		return c
	}
	return cmp.Compare(a.ID, b.ID)
}

// Less 报告 a 的复合键是否严格小于 b。
func Less(a, b Row) bool {
	return Compare(a, b) < 0
}
