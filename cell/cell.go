// Package cell 表示 CSV 字段值及其在原文中的位置。
package cell

// Cell 是一个字段：值、是否加引号、原文中的起止字节偏移（End 为开区间）。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 判断两个字段逐位相等（值、引号标记、偏移全部一致）。
func (c Cell) Equal(o Cell) bool {
	return c.Value == o.Value && c.Quoted == o.Quoted && c.Start == o.Start && c.End == o.End
}

// EqualValue 只比较值与引号标记（忽略偏移）。
func (c Cell) EqualValue(o Cell) bool {
	return c.Value == o.Value && c.Quoted == o.Quoted
}
