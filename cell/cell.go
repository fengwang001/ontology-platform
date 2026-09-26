// Package cell 表示 CSV 记录中的单个字段值。
package cell

// Cell 是一个字段的解析结果：解码后的文本值、是否在原文中加了引号、
// 以及在原始输入中的半开字节区间 [Start, End)。
//
// Quoted 区分「未引号的空字段」（逗号/行尾直接相邻）与
// 「加引号的空字段」（原文为 ""）。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 逐字段比较两个 Cell（值、引号标记、偏移）。
func (c Cell) Equal(o Cell) bool {
	return c == o
}
