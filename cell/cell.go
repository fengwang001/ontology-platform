package cell

// Cell 表示一个 CSV 字段：解码后的字节、是否曾在原文中被引号包裹，
// 以及该字段在原始输入中的半开字节区间 [Start,End)。
// 空字段可区分未引号空（``）与引号空（""），二者 Quoted 不同。
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// Equal 报告两个字段值与引号标记是否逐位相同（偏移不参与）。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && string(c.Value) == string(o.Value)
}
