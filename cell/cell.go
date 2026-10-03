// Package cell 表示 CSV 字段值：区分未加引号空字段与 ""，并记录原文偏移。
package cell

// Cell 是一个字段的解析结果。
type Cell struct {
	Value  string // 解码后的值（"" 已还原为 "）
	Quoted bool   // 原文是否加引号
	Start  int    // 字段在原文中的起始字节偏移（含包围引号），从 0 起
	End    int    // 字段结束偏移（不含，含包围引号）
}

// Equal 报告两个 Cell 是否逐字段相等（含引号标记与偏移）。
func (c Cell) Equal(o Cell) bool {
	return c.Value == o.Value && c.Quoted == o.Quoted && c.Start == o.Start && c.End == o.End
}

// Record 是一条记录（一行）的字段序列。
type Record []Cell

// EqualRecord 比较两条记录。
func EqualRecord(a, b Record) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}
