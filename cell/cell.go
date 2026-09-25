// Package cell 表示一个 CSV 字段值及其在原文中的位置。
package cell

// Cell 是一个字段的解析结果。
// Value 为字段内容（引号字段内的 \r\n 原样保留）。
// Quoted 区分未加引号的空字段与加了引号的空字段 ""。
// Start、End 为该字段在原文中的字节偏移（End 为开区间，从 0 起）。
type Cell struct {
	Value  string
	Quoted bool
	Start  int64
	End    int64
}

// Equal 报告两个 Cell 是否逐字段相同。
func (c Cell) Equal(o Cell) bool {
	return c.Value == o.Value && c.Quoted == o.Quoted && c.Start == o.Start && c.End == o.End
}
