// Package cell 表示 CSV 记录中的一个字段值及其原文定位。
package cell

// Cell 是一个字段。Quoted 区分未引号空字段与加引号空字段 ("")。
// Start/End 是该字段在原始输入中的字节偏移区间 [Start,End)：
// 加引号字段包含两个引号字符本身，未引号空字段 Start==End。
type Cell struct {
	Value  string
	Quoted bool
	Start  int64
	End    int64
}

// Equal 比较两个字段的值与引号标记（偏移不参与语义相等）。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && c.Value == o.Value
}

// New 构造一个字段。
func New(value string, quoted bool, start, end int64) Cell {
	return Cell{Value: value, Quoted: quoted, Start: start, End: end}
}
