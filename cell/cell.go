// Package cell 表示一个 CSV 字段值，并保留引号标记与原文字节偏移。
package cell

// Cell 是一条记录中的一个字段。
// Quoted 区分「未加引号的空字段」与「"" 加引号的空字段」。
// Start/End 为字段在原文中的半开字节区间 [Start, End)：
// 加引号字段包含外层引号；CRLF 行尾的 \r 不计入字段区间。
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

// Equal 报告两个字段值、引号标记是否逐位相同（不比较偏移）。
func (c Cell) Equal(o Cell) bool { return c.Value == o.Value && c.Quoted == o.Quoted }

// EqualOff 在 Equal 基础上还要求偏移相同。
func (c Cell) EqualOff(o Cell) bool { return c.Equal(o) && c.Start == o.Start && c.End == o.End }
