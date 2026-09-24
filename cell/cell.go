// Package cell 表示一个 CSV 字段值及其原文位置。
package cell

// Cell 是一个字段：Value 是反转义后的字段值（引号字段内的 \r\n 原样保留，
// "" 还原为单个 "）；Quoted 区分未引号空字段与 "" 引号空字段；
// Start/End 是该字段在原始输入中的半开字节区间 [Start, End)。
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// Clone 返回值的独立拷贝，避免缓冲区复用导致的别名。
func (c Cell) Clone() Cell {
	v := make([]byte, len(c.Value))
	copy(v, c.Value)
	c.Value = v
	return c
}
