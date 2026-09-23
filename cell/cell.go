// Package cell 表示 CSV 字段值及其原文位置与引号标记。
package cell

// Cell 是一个字段的解析结果。
// Quoted 区分「未加引号的空字段」与「加了引号的空字段 ""」。
// Start/End 为该字段在原始输入中的字节偏移：Start 为首字节位置，
// End 为末字节位置的下一位（半开区间 [Start, End)），空字段二者相等。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Empty 报告字段逻辑值是否为空字符串。
func (c Cell) Empty() bool {
	return len(c.Value) == 0
}
