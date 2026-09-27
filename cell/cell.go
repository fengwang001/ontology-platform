// Package cell 表示 CSV 字段值：可区分未加引号的空字段与加引号的空字段（""），
// 并记录字段在原文中的起止字节偏移（半开区间 [Start,End)）。
package cell

// Cell 是一个已解析完成的字段。
type Cell struct {
	Value  string // 反转义后的字段值（"" -> 一个引号）
	Quoted bool   // 原文字段是否以引号包裹；"" 为 true，裸空字段为 false
	Start  int    // 字段首字节偏移（引号字段为开引号位置）
	End    int    // 字段结束偏移（半开，指向其后的逗号/换行/EOF）
}

// Equal 报告两个字段在值与引号标记上是否逐位相同。
func (c Cell) Equal(o Cell) bool { return c.Quoted == o.Quoted && c.Value == o.Value }

// Span 返回 [Start,End)，便于定位。
func (c Cell) Span() (start, end int) { return c.Start, c.End }
