// Package cell 表示 CSV 字段值及其原文位置与引号标记。
package cell

// Cell 是一个字段。Quoted 区分未引号空字段与 "" 加引号空字段。
// Start/End 为原文中的字节偏移（End 排他，指向闭合引号之后或定界符处）。
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}
