// Package cell 表示一个 CSV 字段的值、引号标记与原文字节偏移。
package cell

// Cell 是一条记录中的一个字段。
// Quoted 为 false 且 Value 为空表示未加引号的空字段；
// Quoted 为 true 且 Value 为空表示原文中的 ""。
// Start/End 为该字段在原文中的半开字节区间 [Start, End)。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}
