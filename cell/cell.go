// Package cell 表示一个 CSV 字段：解码后的值、是否加过引号、原文字节偏移。
package cell

// Cell 是一条记录中的一个字段。
// Quoted 区分「未加引号的空字段」与「加了引号的空字段 ""」。
// Start/End 是该字段在原始输入中的字节偏移区间 [Start, End)：
// 引号字段覆盖首尾引号；未引号空字段是零宽区间。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 报告两个字段逐位相等（值、引号标记、偏移）。
func (c Cell) Equal(o Cell) bool {
	return c.Value == o.Value && c.Quoted == o.Quoted &&
		c.Start == o.Start && c.End == o.End
}

// Record 是一条记录的全部字段。
type Record []Cell

// Equal 报告两条记录逐字段相等。
func (r Record) Equal(o Record) bool {
	if len(r) != len(o) {
		return false
	}
	for i := range r {
		if !r[i].Equal(o[i]) {
			return false
		}
	}
	return true
}

// CellsEqual 报告两个记录切片是否逐位相等。
func CellsEqual(a, b []Record) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}
