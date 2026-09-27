package cell

// Cell 表示一个字段的解码值及其在原文中的位置与引号标记。
type Cell struct {
	Value  string // 解码后的字段内容（引号字段内 \r\n 原样保留）
	Quoted bool   // 原文是否以引号包裹（"" 记为 true）
	Start  int    // 字段首字节偏移（引号字段指向开引号，否则指向首内容字节/槽位）
	End    int    // 字段尾字节偏移（开区间，指向闭合引号后/内容后/槽位处）
}

// Equal 比较两个字段的值与引号标记（位置由调用方需要时另比）。
func (c Cell) Equal(o Cell) bool { return c.Value == o.Value && c.Quoted == o.Quoted }
