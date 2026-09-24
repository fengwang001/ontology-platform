// Package cell 表示一个 CSV 字段值。
package cell

// Cell 是解析出的字段。Bytes 为解码后的字段内容（不含外层引号、"" 已转义还原）。
// Quoted 记录该字段在原文中是否被引号包裹，因此能区分空字段与 ""。
// Start/End 是该字段在原始字节流中的起止偏移：未引号字段 [Start,End)，
// 引号字段含外层两个引号；未加引号的空字段 Start==End，指向分隔位置。
type Cell struct {
	Bytes  []byte
	Quoted bool
	Start  int
	End    int
}

// New 创建一个字段（供 lexer/table 内部与测试使用）。
func New(b []byte, quoted bool, start, end int) Cell {
	return Cell{Bytes: b, Quoted: quoted, Start: start, End: end}
}

// String 返回解码后的字段内容。
func (c Cell) String() string { return string(c.Bytes) }

// Equal 比较两个字段：内容、引号标记与原文偏移逐位相同。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && c.Start == o.Start && c.End == o.End &&
		string(c.Bytes) == string(o.Bytes)
}

// RowsEqual 比较两条记录。
func RowsEqual(a, b []Cell) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}

// TablesEqual 比较两张表。
func TablesEqual(a, b [][]Cell) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !RowsEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}
