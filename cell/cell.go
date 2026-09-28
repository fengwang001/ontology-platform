// Package cell 表示 CSV 字段值：区分未引号空字段与引号空字段，并记录原文字节区间。
package cell

// Cell 是一个字段的值与其在原文中的定位。
// Start 为字段首字节偏移（引号字段指向开头的 "）；End 为字段结束偏移
// （引号字段指向闭合 " 的下一位置，未引号字段指向下一个分隔符位置），半开区间。
// Value 是转义还原后的字段内容（引号字段内的 "" 已还原为单个 "）。
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// Equal 报告两个字段的值与引号标记是否完全相同（不比较偏移）。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && string(c.Value) == string(o.Value)
}

// String 便于演示输出。
func (c Cell) String() string {
	mark := ""
	if c.Quoted {
		mark = "q"
	}
	return mark + string(c.Value)
}
