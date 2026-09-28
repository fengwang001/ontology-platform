package cell

// Cell 表示一个 CSV 字段。Quoted 区分未引号空字段与 ""（加引号的空字段）。
// Start/End 是字段内容在原始字节流中的偏移区间 [Start, End)。
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// Equal 比较字段值与引号标记（偏移属于定位信息，不参与语义相等）。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && string(c.Value) == string(o.Value)
}
