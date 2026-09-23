// Package cell 表示 CSV 字段值：区分未引号空字段与引号空字段 ""，
// 并记录字段在原文中的起止字节偏移 [Start, End)（含外围引号）。
package cell

// Cell 是一条记录中的一个字段。
type Cell struct {
	// Value 是字段的逻辑值（引号已剥离，"" 已还原为 "）。
	Value string
	// Quoted 报告该字段在原文中是否以引号包裹。
	// false 且 Value=="" 是未加引号空字段；true 且 Value=="" 是 ""。
	Quoted bool
	// Start 是字段首字节（引号则为开引号）在原文中的绝对字节偏移。
	Start int
	// End 是字段尾后偏移（引号则为闭引号之后）。
	End int
}

// Equal 逐字段比较值与引号标记（不比较偏移）。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && c.Value == o.Value
}
