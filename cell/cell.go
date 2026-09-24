// Package cell 表示 CSV 字段值及其原文位置。
package cell

// Cell 是一个字段：Value 为解码后内容；Quoted 区分 "" 与空字段；
// Off 为原文 [start,end) 字节区间（含外层引号）。
type Cell struct {
	Value  string
	Quoted bool
	Off    [2]int
}

// New 构造一个字段。
func New(value string, quoted bool, start, end int) Cell {
	return Cell{Value: value, Quoted: quoted, Off: [2]int{start, end}}
}

// Start 返回字段起始字节偏移。
func (c Cell) Start() int { return c.Off[0] }

// End 返回字段结束字节偏移（不含）。
func (c Cell) End() int { return c.Off[1] }
