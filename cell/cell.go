// Package cell 表示 CSV 字段值：内容、是否加引号、原文字节偏移。
package cell

// Cell 是一个字段的解析结果。
type Cell struct {
	Value  string // 字段内容（引号字段内 \r\n 原样保留，"" 还原为 "）
	Quoted bool   // 原文是否加了引号；可区分空字段与 ""
	Start  int    // 原文起始字节偏移（0 起，含开引号）
	End    int    // 原文结束字节偏移（不含闭引号/分隔符）
}

// Equal 比较两个字段逐字段相等（含引号标记）。
func (a Cell) Equal(b Cell) bool {
	return a.Value == b.Value && a.Quoted == b.Quoted && a.Start == b.Start && a.End == b.End
}
