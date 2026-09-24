// Package cell 表示 CSV 字段值及其在原文中的字节偏移与引号标记。
package cell

// Cell 是一个字段的解析结果。
// Quoted 区分未加引号的空字段与加引号的空字段 ""。
// Start/End 为该字段在原始输入中的字节偏移区间 [Start, End)。
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// Equal 比较两个字段的值、引号标记与偏移是否逐位相同。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && c.Start == o.Start && c.End == o.End &&
		string(c.Value) == string(o.Value)
}
