// Package cell 表示 CSV 的单个字段值及其在原文中的位置与引号标记。
package cell

// Cell 是一个字段。Quoted 区分裸空字段与引号空字段 ""。
// Start/End 是该字段在原始字节流中的半开区间 [Start, End)，
// 引号字段包含两端引号；CRLF 行尾不计入前一字段的 End。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 逐字段比较值与引号标记（不比较偏移）。
func (c Cell) Equal(o Cell) bool { return c.Value == o.Value && c.Quoted == o.Quoted }
