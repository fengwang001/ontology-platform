// Package cell 表示 CSV 字段值：区分空字段是否被引号包裹，并记录原文字节偏移。
package cell

// Cell 是一个字段。Quoted=false 表示未加引号的字段（包括空字段），
// Quoted=true 表示原文以 "..." 包裹（包括加了引号的空字段 ""）。
// Start/End 是该字段在原始输入中的字节偏移区间 [Start,End)；
// 引号字段的 Start 指向开引号，End 指向闭引号之后。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 报告两个字段逐字段相等：值与引号标记都相同。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && c.Value == o.Value && c.Start == o.Start && c.End == o.End
}
