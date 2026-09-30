// Package cell 表示 CSV 的一个字段值。
//
// 偏移约定（记录字段在原文中的起止字节偏移，从 0 起）：
//   - Start：字段首个原始字节的偏移（加引号字段指向开头的引号）；
//   - End：字段末个原始字节之后的偏移（加引号字段指向闭合引号之后）。
//
// 未加引号的空字段与加引号的空字段（""）用 Quoted 区分。
package cell

// Cell 是一个已解析字段。
type Cell struct {
	Value  string // 反转义后的字段内容（引号字段内的 \r\n 原样保留）
	Quoted bool   // 原文是否用引号包裹
	Start  int    // 起始字节偏移（含）
	End    int    // 结束字节偏移（不含）
}

// Equal 报告两个字段逐字段相等（值、引号标记、偏移）。
func (c Cell) Equal(o Cell) bool {
	return c == o
}
