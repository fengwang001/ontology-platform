// Package cell 表示一个 CSV 字段值及其原文形态。
package cell

// Cell 是一个字段：字节值、是否以引号包裹、原文中的起止字节偏移（End 为排他）。
// Quoted 区分「未加引号的空字段」(Quoted=false) 与「""」(Quoted=true)。
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// String 返回字段值的字符串形式，便于打印。
func (c Cell) String() string { return string(c.Value) }
