// Package cell 表示一个 CSV 字段值：内容、是否带引号、原文字节偏移。
package cell

// Cell 是一个字段。Quoted 区分未加引号空字段与 "" 加引号空字段。
// OffStart/OffEnd 为原文中的半开字节区间 [OffStart, OffEnd)，指向引号外围。
type Cell struct {
	Value    string
	Quoted   bool
	OffStart int
	OffEnd   int
}

// Equal 比较两个字段的值与引号标记（偏移不参与逻辑相等）。
func Equal(a, b Cell) bool { return a.Value == b.Value && a.Quoted == b.Quoted }
