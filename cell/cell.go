// Package cell 表示 CSV 字段值及其在原文中的字节位置与引号标记。
package cell

// Cell 是一条记录中的单个字段。
//
// Value 为解码后的字段内容（引号字段内的 "" 已还原为一个 "，
// 字段内的 \r\n 原样保留）。Quoted 报告该字段在原文中是否带引号，
// 从而区分未加引号的空字段与 ""。Start/End 为该字段在原始字节流中
// 的半开区间 [Start,End)：带引号时包含首尾两个引号字节。
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// String 返回字段解码后的字符串形式。
func (c Cell) String() string { return string(c.Value) }

// Clone 返回 Value 不与任何解析缓冲共享的深拷贝，便于长期持有。
func (c Cell) Clone() Cell {
	v := make([]byte, len(c.Value))
	copy(v, c.Value)
	c.Value = v
	return c
}

// Equal 报告两个字段的值、引号标记与偏移是否逐位相同。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && c.Start == o.Start && c.End == o.End &&
		string(c.Value) == string(o.Value)
}
