// Package cell 表示一个 CSV 字段值及其在原文中的位置与引号形态。
package cell

// Cell 是一条记录中的一个字段。
// Start/End 是原文中的半开字节区间 [Start,End)：
// 未引号字段覆盖其全部字节（空字段时 Start==End，指向该字段所在位置）；
// 引号字段覆盖首尾两个引号（空引号字段 "" 时 End==Start+2）。
// Quoted 区分「未加引号的空字段」与「""」。
type Cell struct {
	Value  string
	Start  int
	End    int
	Quoted bool
}

// Equal 判断两个字段值与引号标记是否逐位相等（不比较偏移）。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && c.Value == o.Value
}

// Table 是解析成功的表：Header 为第一条记录，Rows 为其余记录。
// 空文件时 Header == nil 且 Rows == nil。
type Table struct {
	Header []Cell
	Rows   [][]Cell
}
