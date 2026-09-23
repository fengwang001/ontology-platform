// Package cell 表示 CSV 一个字段的值、是否加过引号以及原文字节偏移。
package cell

// Cell 是一个字段。Value 为反转义后的字段内容；Quoted 区分
// 「未加引号的空字段」与「加了引号的空字段 ""」。
// Start/End 为该字段在原始输入中的字节区间 [Start,End)：
// 引号字段包含外层引号，未引号字段不含终结逗号/换行。
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// New 构造一个字段。
func New(value []byte, quoted bool, start, end int) Cell {
	return Cell{Value: value, Quoted: quoted, Start: start, End: end}
}

// Equal 报告两个字段逐值相等且引号标记相同。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && string(c.Value) == string(o.Value)
}
