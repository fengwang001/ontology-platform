// Package cell 表示 CSV 字段值及其原文位置。
package cell

// Cell 是一个字段的解析结果。
// Quoted 区分未引号空字段与加引号空字段 ""。
// Start/End 为该字段在原始输入中的字节偏移区间 [Start, End)：
// 引号字段含首尾引号；未引号空字段 Start==End，指向分隔符位置。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 比较两个字段的值与引号标记（不比较偏移）。
func (c Cell) Equal(o Cell) bool { return c.Value == o.Value && c.Quoted == o.Quoted }

// Clone 返回脱离原缓冲区的独立副本。
func (c Cell) Clone() Cell {
	return Cell{Value: string(append([]byte(nil), c.Value...)), Quoted: c.Quoted, Start: c.Start, End: c.End}
}
