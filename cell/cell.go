// Package cell 表示 CSV 单个字段的值、引号标记与原文字节偏移。
package cell

// Cell 是一个解析完成的字段。
// Quoted 区分「未加引号的空字段」与「加了引号的空字段 ""」。
// Start/End 是该字段在原始输入中的字节偏移区间 [Start, End)：
// 未引号字段 End 指向终结符（逗号/换行）所在字节；
// 引号字段 End 指向闭合引号之后第一个字节。
// 跨并行切分拼接的字段，Start 取首个源段起点，End 取末段源区间。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 比较两个字段是否逐位相等（值与引号标记都相同；偏移不参与）。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && c.Value == o.Value
}
