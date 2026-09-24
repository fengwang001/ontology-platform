// Package cell 表示 CSV 的一个字段：值、是否曾被引号包裹、原文字节偏移。
package cell

// Cell 是一个字段。Quoted 区分未引号空字段与 ""（加引号空字段）。
// Start/End 为该字段在原始输入中的字节偏移区间 [Start, End)：
// 引号字段含外层引号，未引号字段为值字节本身，空字段时 Start==End。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 报告两个字段（含引号标记与偏移）逐位相同。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && c.Start == o.Start && c.End == o.End && c.Value == o.Value
}
