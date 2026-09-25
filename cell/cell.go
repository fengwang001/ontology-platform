// Package cell 表示 CSV 中的单个字段值。
package cell

// Cell 是一个字段：区分未引号空字段与加引号的空字段 ""，
// 并记录其在原文中的字节偏移 [Start,End)。
type Cell struct {
	Value  string
	Quoted bool
	Start  int // 字段首字节偏移（引号字段指向开引号），从 0 起
	End    int // 字段末字节的下一位置（逗号/换行位置，无尾换行时为流长度）
}

// Equal 比较两个字段的值与引号标记（不比较偏移）。
func (c Cell) Equal(o Cell) bool { return c.Value == o.Value && c.Quoted == o.Quoted }

// New 构造一个字段。
func New(value string, quoted bool, start, end int) Cell {
	return Cell{Value: value, Quoted: quoted, Start: start, End: end}
}
