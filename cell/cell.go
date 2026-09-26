// Package cell 表示一个 CSV 字段的解析结果。
package cell

// Cell 是一个字段：逻辑值、是否曾被引号包裹、原文起止字节偏移。
// Start/End 为相对整份输入的字节偏移（从 0 起），End 不含引号本身：
// 无引号字段 [Start,End) 即原始字节；引号字段 [Start,End) 为去掉两端引号、
// 已把 "" 折叠为 " 后的逻辑值在原文中首个内容字节的位置到末个内容字节之后。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 报告两个字段逐字段相等（值与引号标记）。
func (c Cell) Equal(o Cell) bool { return c.Quoted == o.Quoted && c.Value == o.Value }
