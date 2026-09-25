// Package cell 表示 CSV 记录中的一个字段值。
package cell

// Cell 是一条记录中的单个字段。
// Quoted 区分「未加引号的空字段」与加引号的空字段 ""。
// Start/End 为该字段在原文中的字节偏移：Start 指向首字节
//（引号字段指向开头引号），End 为结束字节的后一位置（半开区间）。
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// Record 是一条记录的全部字段。
type Record = []Cell

// Empty 返回未加引号的空字段，偏移为 [pos,pos)。
func Empty(pos int) Cell { return Cell{Start: pos, End: pos} }

// New 构造一个字段。
func New(value []byte, quoted bool, start, end int) Cell {
	return Cell{Value: value, Quoted: quoted, Start: start, End: end}
}
