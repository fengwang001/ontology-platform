// Package cell 表示一个 CSV 字段值及其原文形态。
package cell

// Cell 是一条记录中的一个字段。
// Quoted 区分「未加引号的空字段」与「加了引号的空字段 ""」。
// Start/End 为该字段在原文中的字节偏移区间 [Start, End)：
// 未引号字段覆盖其全部内容字节；引号字段覆盖含首尾引号的原文。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 比较两个字段的值与引号标记（偏移不参与语义相等）。
func (c Cell) Equal(o Cell) bool { return c.Value == o.Value && c.Quoted == o.Quoted }
