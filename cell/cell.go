// Package cell 表示 CSV 字段值及其在原文中的定位。
package cell

// Cell 是一个字段的解析结果。
// Quoted 区分未加引号的空字段与 "​"（加了引号的空字段）。
// Start/End 是该字段在原始输入中的半开字节区间 [Start, End)：
// 未引号字段覆盖原始字节；引号字段覆盖含两侧引号在内的整段原文。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 报告两个字段逐位相等（值与引号标记都相同）。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && c.Value == o.Value && c.Start == o.Start && c.End == o.End
}
