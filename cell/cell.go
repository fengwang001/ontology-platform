package cell

// Cell 表示一个字段的值、引号标记与原文字节偏移。
// Quoted 区分「未引号空字段」(false) 与「""」(true)。
// Start 为字段首字节偏移；End 为字段最后一个内容字节偏移 +1（半开区间）。
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// Equal 逐字段比较值与引号标记（偏移不参与语义相等）。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && string(c.Value) == string(o.Value)
}
