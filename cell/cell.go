// Package cell 表示一个 CSV 字段的值、引号标记与原文字节偏移。
package cell

// Cell 是一个已解析字段。
type Cell struct {
	Value  string // 解码后的字段内容（"" 折叠为 "，字段内 \r\n 原样保留）
	Quoted bool   // 原文是否以引号包裹；"" 为 true，裸空字段为 false
	Start  int    // 字段首字节偏移（含开引号）
	End    int    // 字段末字节的下一个偏移（含闭引号之后）
}

// Equal 报告两个字段逐位相同（值、引号标记、偏移）。
func (c Cell) Equal(o Cell) bool {
	return c == o
}
