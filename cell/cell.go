// Package cell 表示 CSV 字段值及其在原文中的字节位置。
package cell

// Cell 是一个字段：内容（转义还原后的字节）、引号标记、原文起止偏移。
// Start 为字段首字节偏移（引号字段指向开头的 "）；End 为字段末字节的下一偏移
// （不含终止的逗号/换行）。Quoted 区分未引号空字段与 ""。
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// Clone 返回深拷贝，避免多个分片拼接时共享底层数组。
func (c Cell) Clone() Cell {
	v := make([]byte, len(c.Value))
	copy(v, c.Value)
	c.Value = v
	return c
}

// Equal 逐字段比较值与引号标记（偏移不参与，仅用于内容等价判断）。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && string(c.Value) == string(o.Value)
}
