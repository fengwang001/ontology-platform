// Package cell 表示一个 CSV 字段的解析结果。
package cell

// Cell 是一个字段：值、是否以引号形式书写、在原始字节流中的起止偏移。
// 未加引号的空字段（a,,b 中间）与加引号的空字段（""）由 Quoted 区分。
type Cell struct {
	Value  []byte // 解码后的字段内容（引号字段内的 "" 已折叠为 "）
	Quoted bool   // 原文是否以引号包裹
	Off    int    // 字段首字节偏移（引号字段为开引号位置）
	End    int    // 字段末字节之后的偏移（引号字段为闭引号之后）
}

// Clone 返回不与任何输入缓冲区共享底层数组的副本。
func (c Cell) Clone() Cell {
	v := make([]byte, len(c.Value))
	copy(v, c.Value)
	c.Value = v
	return c
}
