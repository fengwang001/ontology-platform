// Package cell 表示一个 CSV 字段的解析结果。
package cell

// Cell 是一条记录中的一个字段。
// Value 为解码后的字段值；Quoted 记录原文是否带引号，
// 因而未引号空字段与 "" 可区分。
// Start/End 为该字段在原文中的字节偏移，[Start,End)。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}
