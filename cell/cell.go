package cell

// Cell 表示一个 CSV 字段的解析结果。
// Quoted 区分「未引号空字段」与「引号空字段 ""」。
// Start/End 是字段在原始输入中的半开字节区间，含两侧引号（若有）。
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// Clone 返回深拷贝，避免调用方与解析器内部缓冲别名。
func (c Cell) Clone() Cell {
	v := make([]byte, len(c.Value))
	copy(v, c.Value)
	c.Value = v
	return c
}
