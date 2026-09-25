// Package cell 表示一个 CSV 字段值及其原文位置与引号标记。
package cell

// Cell 是一个字段。Quoted 区分未引号空字段与加引号的空字段 ""。
// Start/End 是原文中的字节偏移区间 [Start,End)。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 逐字段比较值与引号标记（偏移不参与）。
func (c Cell) Equal(o Cell) bool { return c.Value == o.Value && c.Quoted == o.Quoted }

// Limits 是可配置的解析上限。零值表示不限。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}
