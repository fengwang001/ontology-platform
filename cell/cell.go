// Package cell 表示一个 CSV 字段值。
package cell

// Cell 是一条记录中的一个字段。
// Quoted 区分未加引号的空字段与加引号的空字段 ""。
// Start/End 是该字段在原文中的字节偏移区间 [Start, End)，
// 含定界引号；无任何字节（EOF 空输入）时 Start==End==0。
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// Equal 逐字段比较值与引号标记。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && string(c.Value) == string(o.Value)
}
