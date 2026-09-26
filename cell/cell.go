// Package cell 表示 CSV 字段值及其原文形态。
package cell

// Cell 是一条记录中的一个字段。
// Quoted 区分「未加引号的空字段」与「加引号的空字段 ""」。
// Start/End 为字段在原文中的字节偏移：Start 指向首字节，
// End 指向字段末字节的下一位置（半开区间 [Start,End)）。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 逐字段比较值与引号标记（偏移不参与语义相等）。
func (c Cell) Equal(o Cell) bool { return c.Value == o.Value && c.Quoted == o.Quoted }
