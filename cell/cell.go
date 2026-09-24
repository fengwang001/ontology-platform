// Package cell 表示一个 CSV 字段值：内容、是否加引号、在原文中的字节区间。
package cell

// Cell 是一个字段的解析结果。
// Start、End 是该字段在原文中的字节区间 [Start, End)：
// 未引号字段为内容本身；引号字段含两侧引号（空引号字段 Start==End，指向第一个引号）。
type Cell struct {
	Value  string // 解码后的字段内容（引号字段内 "" 已还原为 "）
	Quoted bool   // 原文是否用引号包裹；区分空字段与 ""
	Start  int    // 起始字节偏移（从 0 起）
	End    int    // 结束字节偏移（不含）
}

// Equal 逐字段比较两个 Cell（值、引号标记、偏移全部相等）。
func (c Cell) Equal(o Cell) bool {
	return c.Value == o.Value && c.Quoted == o.Quoted && c.Start == o.Start && c.End == o.End
}
