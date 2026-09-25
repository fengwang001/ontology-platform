package cell

// Cell 表示一个字段的解析结果。
// Quoted 区分未引号空字段与引号空字段 ""。
// Start/End 为该字段在原始输入中的字节偏移区间 [Start,End)：
// 引号字段含外侧两个引号，未引号字段为裸值本身。
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// String 返回字段值字符串。
func (c Cell) String() string { return string(c.Value) }
