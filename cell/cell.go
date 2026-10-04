// Package cell 表示 CSV 字段值，区分未加引号的空字段与加引号的空字段，
// 并记录字段在原文中的起止字节偏移。不依赖其他包。
package cell

// Cell 是一个字段的解析结果。
type Cell struct {
	Val    string // 解码后的字段内容（引号字段内 "" 已还原为 "）
	Quoted bool   // 原文中该字段是否被引号包围
	Start  int    // 字段在原文中的起始字节偏移（含引号），从 0 起
	End    int    // 字段在原文中的结束字节偏移（不含，含引号）
}

// Equal 判断两个字段是否逐位相等（内容、引号标记、偏移全部相同）。
func (c Cell) Equal(o Cell) bool {
	return c.Val == o.Val && c.Quoted == o.Quoted && c.Start == o.Start && c.End == o.End
}

// Record 是一条记录，即一串字段。
type Record []Cell

// Equal 判断两条记录是否逐字段相等。
func (r Record) Equal(o Record) bool {
	if len(r) != len(o) {
		return false
	}
	for i := range r {
		if !r[i].Equal(o[i]) {
			return false
		}
	}
	return true
}

// NeedsQuote 报告按最小引号规则回写时该字段是否必须加引号：
// 含逗号、引号、\r、\n，或「空且未加引号」——后者写成空行会被解析器
// 当作空行跳过，导致单列表丢记录（见 DESIGN.md 第一节）。
func (c Cell) NeedsQuote() bool {
	if c.Val == "" {
		return true
	}
	for i := 0; i < len(c.Val); i++ {
		switch c.Val[i] {
		case ',', '"', '\r', '\n':
			return true
		}
	}
	return false
}
