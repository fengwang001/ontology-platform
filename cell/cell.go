// Package cell 表示一个 CSV 字段：值、是否加过引号、原文字节偏移。
package cell

// Cell 是一个字段的解码结果。
// Start/End 为该字段在原始字节流中的半开区间 [Start,End)，
// 加引号字段包含包裹它的两个引号；Quoted 区分 "" 与空的未引号字段。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 逐字段比较值与引号标记（偏移不参与）。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && c.Value == o.Value
}
