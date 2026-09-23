// Package cell 表示 CSV 记录中的单个字段。
package cell

// Cell 是一个字段的值及其在原始字节流中的定位。
// Start/End 为半开区间 [Start, End)，指向字段在原文中的字节范围；
// 对引号字段，区间包含外层引号；转义引号 "" 占两个源字节。
type Cell struct {
	Value  []byte
	Quoted bool // 原文是否带引号；"" 与裸空字段借此区分
	Start  int64 // 字段首字节偏移（含开引号）
	End    int64 // 字段末字节的下一偏移（含闭引号）
}

// Clone 返回值的独立拷贝，避免调用方与解析器内部缓冲别名。
func (c Cell) Clone() Cell {
	v := make([]byte, len(c.Value))
	copy(v, c.Value)
	c.Value = v
	return c
}

// Equal 报告两个字段逐字段相等且引号标记相同。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && string(c.Value) == string(o.Value)
}
