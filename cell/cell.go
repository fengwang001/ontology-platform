// Package cell 表示 CSV 字段值：内容、是否加过引号、原文字节偏移。
package cell

// Cell 是一个字段的解析结果。
// Quoted 区分「未加引号的空字段」与「加引号的空字段 ""」。
// Start/End 是该字段在原始输入中的字节偏移区间 [Start, End)。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 逐字段比较值与引号标记（偏移不参与）。
func (c Cell) Equal(o Cell) bool { return c.Value == o.Value && c.Quoted == o.Quoted }

// Record 是一条记录的字段序列。
type Record []Cell

// Equal 比较两条记录是否逐字段相等（含引号标记）。
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
