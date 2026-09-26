// Package cell 表示 CSV 字段值，区分加引号与未引号的空字段，并记录原文字节区间。
package cell

// Cell 是一个字段。Quoted 表示该字段在原文中是否以引号形式出现（"" 也算 true）。
// Start/End 是该字段原文词法片段在输入中的半开字节区间 [Start, End)：
// 加引号字段含两侧引号；未引号字段即其原始字节。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 比较两个 cell 的值与引号标记是否逐位相同（偏移不参与语义相等）。
func (c Cell) Equal(o Cell) bool { return c.Value == o.Value && c.Quoted == o.Quoted }
