// Package cell 表示 CSV 中的一个字段值及其原文信息。
package cell

// Cell 是一个字段的值。
//
// Quoted 区分两种空字段：未引用空字段（a,,b 中间的空）与
// 引用空字段（""）。Start/End 是该字段在原始字节流中的半开区间
// [Start, End)；跨 par 切点的字段只由覆盖其闭引号的段输出。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 比较两个字段的值与引号标记是否逐位相同。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && c.Value == o.Value
}

// Record 是一条记录的全部字段。
type Record []Cell

// Equal 比较两条记录（字段值、引号标记、偏移）。
func (r Record) Equal(o Record) bool {
	if len(r) != len(o) {
		return false
	}
	for i := range r {
		if r[i] != o[i] {
			return false
		}
	}
	return true
}
