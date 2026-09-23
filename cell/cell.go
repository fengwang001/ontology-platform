// Package cell 表示 CSV 记录中的单个字段值及其原文形态。
package cell

// Cell 是一个字段：值、是否在原文中被引号包裹、原文起止字节偏移。
// Start/End 为全局字节偏移，区间 [Start, End) 包含开引号与闭引号。
type Cell struct {
	Value  string
	Quoted bool
	Start  int64
	End    int64
}

// New 构造一个未加引号的字段。
func New(value string, start, end int64) Cell {
	return Cell{Value: value, Start: start, End: end}
}

// NewQuoted 构造一个加引号的字段（含空引号字段 ""）。
func NewQuoted(value string, start, end int64) Cell {
	return Cell{Value: value, Quoted: true, Start: start, End: end}
}

// IsEmpty 报告字段是否为空字符串；空字符串仍可通过 Quoted 区分两种原文形态。
func (c Cell) IsEmpty() bool { return len(c.Value) == 0 }
