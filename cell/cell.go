// Package cell 表示 CSV 字段值及其原文位置。
package cell

// Cell 是一个字段：值、是否在原文中带引号、原文字节区间 [Start,End)。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 逐字段比较（值与引号标记）。
func (c Cell) Equal(o Cell) bool {
	return c.Value == o.Value && c.Quoted == o.Quoted && c.Start == o.Start && c.End == o.End
}
