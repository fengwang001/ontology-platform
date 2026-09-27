// Package cell 表示 CSV 记录中的一个字段及其原文字节位置。
package cell

// Cell 是一个字段值。Quoted 区分「未引号空字段」与「引号空字段 ""」。
// Start/End 是该字段在原始输入中的字节偏移区间 [Start,End)，
// 引号字段含外层两个引号，未引号字段为其裸字节区间。
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// Equal 逐字段比较值与引号标记（偏移不参与，用于往返比较）。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && string(c.Value) == string(o.Value)
}
