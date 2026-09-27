// Package cell 表示 CSV 记录中的单个字段值。
package cell

// Cell 是一个字段的解析结果。
// Quoted 区分「未加引号的空字段」与「加了引号的空字段 ""」。
// Start/End 是该字段在原始输入中的字节偏移区间 [Start,End)，
// 指向包含引号、转义在内的原始字节。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// New 构造一个字段。
func New(value string, quoted bool, start, end int) Cell {
	return Cell{Value: value, Quoted: quoted, Start: start, End: end}
}

// Equal 报告两个字段逐字段相等（值、引号标记、偏移均相同）。
func (c Cell) Equal(o Cell) bool {
	return c.Value == o.Value && c.Quoted == o.Quoted &&
		c.Start == o.Start && c.End == o.End
}
