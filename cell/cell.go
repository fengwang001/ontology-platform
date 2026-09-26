// Package cell 表示一个 CSV 字段值及其在原文中的位置。
package cell

// Cell 是一个字段的解析结果。Quoted 区分「未加引号的空字段」
// 与「加了引号的空字段 ""」；Start/End 是原文中的字节偏移
// （含分隔引号，半开区间 [Start, End)）。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 报告两个 Cell 的值与引号标记是否相同（忽略偏移）。
func (c Cell) Equal(o Cell) bool { return c.Value == o.Value && c.Quoted == o.Quoted }
