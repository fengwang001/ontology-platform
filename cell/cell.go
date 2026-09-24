// Package cell 表示 CSV 记录中的一个字段值。
package cell

// Cell 是一个字段的解析结果。
// Val 为字段内容（引号字段内的转义已还原，字段内 \r\n 原样保留）。
// Quoted 为 true 表示该字段在原文中带引号（"" 与未加引号的空字段由此区分）。
// Start、End 是该字段在原文中的字节区间 [Start,End)，含定界引号，从 0 起。
type Cell struct {
	Val    []byte
	Quoted bool
	Start  int
	End    int
}

// Equal 报告两个字段是否逐位相等（内容、引号标记、偏移全部相同）。
func (c Cell) Equal(o Cell) bool {
	if c.Quoted != o.Quoted || c.Start != o.Start || c.End != o.End {
		return false
	}
	if len(c.Val) != len(o.Val) {
		return false
	}
	for i := range c.Val {
		if c.Val[i] != o.Val[i] {
			return false
		}
	}
	return true
}

// New 构造一个字段（供 writer 调用方手工建表时使用）。
func New(val []byte, quoted bool) Cell {
	return Cell{Val: val, Quoted: quoted}
}
