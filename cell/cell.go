// Package cell 表示 CSV 记录中的一个字段值。
package cell

// Cell 是一个字段。Quoted 区分未加引号的空字段与 ""（加引号的空字段）。
// Start/End 是该字段在原始输入中的字节偏移区间 [Start,End)，
// 加引号时包含两个引号字符；未加引号空字段时 Start==End。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 比较两个 Cell 的值、引号标记与原文偏移。
func (c Cell) Equal(o Cell) bool {
	return c == o
}
