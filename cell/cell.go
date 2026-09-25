// Package cell 表示 CSV 字段值。
//
// Quoted 区分「未加引号的空字段」与「加了引号的空字段 ""」；
// Start/End 是该字段在原始输入中的字节偏移区间 [Start, End)。
package cell

// Cell 是一个字段的解析结果。
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// Clone 返回与底层数组无关的副本。
func (c Cell) Clone() Cell {
	v := make([]byte, len(c.Value))
	copy(v, c.Value)
	c.Value = v
	return c
}

// Equal 报告两个字段的值与引号标记是否逐位相同。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && string(c.Value) == string(o.Value)
}
