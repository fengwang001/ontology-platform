// Package cell 描述 CSV 字段值：原始字节内容、是否加引号、原文字节偏移。
package cell

// Cell 是一条记录中的一个字段。
// Quoted 区分「未加引号的空字段」与「加了引号的空字段 ""」。
// Start/End 是字段在原始输入中的字节范围：[Start, End)，
// 加引号字段包含两侧引号；End 是字段最后一个内容字节的后一位置
// （引号字段为闭引号后一位置，不含逗号/换行）。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Clone 返回深拷贝，避免调用方持有底层缓冲造成串扰。
func (c Cell) Clone() Cell {
	return Cell{Value: string(append([]byte(nil), c.Value...)), Quoted: c.Quoted, Start: c.Start, End: c.End}
}

// Equal 按值、引号标记、偏移逐项比较。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && c.Start == o.Start && c.End == o.End && c.Value == o.Value
}

// Record 是一条记录的全部字段。
type Record []Cell

// Equal 比较两条记录逐字段相等（含引号标记与偏移）。
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

// EqualRows 比较记录集合。
func EqualRows(a, b []Record) bool {
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
