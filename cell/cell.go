// Package cell 表示 CSV 字段值及其原文形态。
package cell

// Cell 是一条记录中的一个字段。Start/End 为原文中的字节偏移（左闭右开）。
// Quoted 区分未引号空字段与加引号的空字段（""）。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 逐字段比较值与引号标记（偏移由调用方按需另比）。
func (c Cell) Equal(o Cell) bool { return c.Value == o.Value && c.Quoted == o.Quoted }
