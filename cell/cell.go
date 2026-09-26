// Package cell 表示 CSV 的一个字段值及其原文形态。
// 不依赖本工程的其他包。
package cell

// Cell 是一条记录中的单个字段。
type Cell struct {
	// Value 是反转义后的字段内容；引号字段内的 "" 已还原为单个 "。
	Value []byte
	// Quoted 报告该字段在原文中是否带引号。
	// 因此未引号的空字段与 "" 可区分：二者 Value 均为空，Quoted 不同。
	Quoted bool
	// Start 是字段在原始输入中的起始字节偏移（含开引号），从 0 起。
	Start int
	// End 是字段在原始输入中的结束字节偏移（不含）。
	// 引号字段的 End 指向闭引号之后；未引号字段指向分隔符或行尾。
	End int
}

// Clone 返回与 c 不共享底层数组的副本。
func (c Cell) Clone() Cell {
	v := make([]byte, len(c.Value))
	copy(v, c.Value)
	return Cell{Value: v, Quoted: c.Quoted, Start: c.Start, End: c.End}
}

// IsEmpty 报告字段是否为空值（两种引号形态都算）。
func (c Cell) IsEmpty() bool { return len(c.Value) == 0 }
