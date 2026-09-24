// Package cell 表示 CSV 中的一个字段值：可区分未引号空字段与引号空字段 ""，
// 并记录该字段在原文中的起止字节偏移（半开区间 [Start, End)）。
package cell

// Cell 是一个字段的解析结果。
type Cell struct {
	Value  string // 逻辑内容（"" 已还原为一个 "；字段内 \r\n 原样保留）
	Quoted bool   // 是否以引号定界；`""` 为 true，未加引号的空字段为 false
	Start  int    // 字段首字节偏移（引号字段指向开头的引号）
	End    int    // 字段结束后的字节偏移（引号字段指向闭合引号之后）
}

// Equal 逐字段比较两个 Cell（值与引号标记；偏移不影响相等判定）。
func (c Cell) Equal(o Cell) bool { return c.Value == o.Value && c.Quoted == o.Quoted }
