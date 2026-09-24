// Package cell 表示 CSV 记录中的一个字段值及其原文定位。
package cell

// Cell 是一个字段。Quoted 区分「未引号空字段」与「引号空字段 ""」。
// Start/End 是字段在原始输入中的字节偏移区间 [Start,End)：
// 引号字段含外层引号；未引号字段就是值本身；空未引号字段 Start==End。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 比较两个单元格的值与引号标记（偏移不参与）。
func (c Cell) Equal(o Cell) bool { return c.Quoted == o.Quoted && c.Value == o.Value }
