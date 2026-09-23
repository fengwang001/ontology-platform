// Package cell 表示 CSV 字段值与其在原文中的字节偏移。
package cell

// Cell 是一个字段。Quoted 区分未加引号的空字段与 "" 加引号的空字段。
// Start/End 为该字段在原始输入中的字节偏移（End 不含，指向逗号/换行位置）。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Record 是一条记录的字段序列。
type Record []Cell

// Equal 报告两条记录是否逐字段相等（值与引号标记、偏移都相同）。
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
