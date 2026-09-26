// Package cell 表示 CSV 字段值及其在原文中的位置与引号标记。
package cell

// Cell 是一个字段。Start/End 为原文字节偏移区间 [Start,End)。
// Quoted 区分未引号空字段与加引号的空字段 ""。
type Cell struct {
	Value  string
	Quoted bool
	Start  int64
	End    int64
}

// Equal 逐字段比较值与引号标记（不比较偏移）。
func (c Cell) Equal(o Cell) bool { return c.Value == o.Value && c.Quoted == o.Quoted }

// Record 是一条记录的全部字段。
type Record []Cell

// RowsEqual 比较两条记录的每个 Cell。
func RowsEqual(a, b Record) bool {
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
