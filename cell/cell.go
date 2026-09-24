// Package cell 表示 CSV 记录中的一个字段值。
package cell

// Cell 是一个字段的解码值及其在原始字节流中的定位。
type Cell struct {
	// Value 是字段的解码内容（引号字段内的 "" 已还原为 "）。
	Value string
	// Quoted 为 true 表示该字段在原文中以引号包裹（"" 也算）。
	Quoted bool
	// Start 是字段第一个字节（引号字段为开引号）的全局字节偏移。
	Start int
	// End 是字段最后一个字节（引号字段为闭引号）之后的字节偏移。
	End int
}

// Equal 逐字段比较值、引号标记与偏移。
func (c Cell) Equal(o Cell) bool {
	return c == o
}
