// Package cell 表示 CSV 字段值及其在原文中的位置。
package cell

// Cell 是一个字段。Start/End 为原文半开字节区间 [Start,End)。
// Quoted 区分未引号空字段与加引号的空字段 ""。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}
