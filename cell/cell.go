// Package cell 表示一个 CSV 字段值及其在原文中的位置。
package cell

// Cell 是一个字段。Quoted 区分「未引号空字段」与「引号空字段 ""」。
// Start/End 为字段在原始输入中的字节偏移 [Start,End)，指向字段第一个
// 内容字节；引号字段从开引号算起，到闭引号之后。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 比较两个字段的值与引号标记（不比较偏移）。
func (c Cell) Equal(o Cell) bool { return c.Value == o.Value && c.Quoted == o.Quoted }
