// Package cell 表示一个 CSV 字段值及其原文定位与引号标记。
package cell

// Cell 是一个字段的解码结果。
// Quoted 区分「未加引号的空字段」与「加了引号的空字段 ""」。
// Start/End 是该字段在原始输入中的字节偏移区间 [Start, End)。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 比较两个字段的值与引号标记是否完全相同（不比较偏移）。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && c.Value == o.Value
}
