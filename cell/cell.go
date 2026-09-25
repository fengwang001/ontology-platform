// Package cell 表示 CSV 中的一个字段值。
package cell

// Cell 是一个字段：值、是否以引号书写、原文起止字节偏移（半开区间）。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 比较值与引号标记（偏移不参与语义相等）。
func (c Cell) Equal(o Cell) bool { return c.Value == o.Value && c.Quoted == o.Quoted }
