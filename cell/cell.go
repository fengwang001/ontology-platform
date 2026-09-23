// Package cell 表示 CSV 字段值：保留引号标记与原文字节偏移。
package cell

// Cell 是一个字段的解析结果。
type Cell struct {
	Value  string // 反转义后的字段值（引号字段内的 \r\n 原样保留）
	Quoted bool   // 原文是否以引号包裹（"" 与裸空字段借此区分）
	Start  int    // 字段首字节在输入中的偏移（含起始引号）
	End    int    // 字段末字节之后的偏移（不含逗号/换行；引号字段含关闭引号）
}

// Clone 返回脱离底层缓冲的深拷贝，保证多次解析结果互不串扰。
func (c Cell) Clone() Cell {
	return Cell{Value: string(append([]byte(nil), c.Value...)), Quoted: c.Quoted, Start: c.Start, End: c.End}
}
