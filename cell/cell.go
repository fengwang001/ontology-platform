// Package cell 表示 CSV 的单个字段值及其原文元数据。
package cell

// Cell 是一个字段。Quoted 区分未加引号的空字段与 ""（加引号的空字段）。
// Start/End 为该字段在原文中的字节偏移区间 [Start, End)。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 逐字段比较值与引号标记。
func (c Cell) Equal(o Cell) bool {
	return c.Value == o.Value && c.Quoted == o.Quoted
}
