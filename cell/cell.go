// Package cell 表示一个 CSV 字段的值、引号标记与原文字节偏移。
package cell

// Cell 是一条记录中的一个字段。
// Quoted 区分「未加引号的空字段」与「加了引号的空字段 ""」。
// Start/End 为该字段在原始输入中的字节偏移 [Start,End)，
// 未引号空字段（零宽度）Start==End；引号空字段 "" 宽度为 2。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 比较两个字段的值与引号标记（偏移不参与语义相等）。
func (c Cell) Equal(o Cell) bool { return c.Value == o.Value && c.Quoted == o.Quoted }
