// Package cell 表示 CSV 字段值及其原文位置与引号标记。
package cell

// Cell 是一个字段：解码后的值、是否以引号书写、原文 [Start,End) 字节偏移。
// 引号字段的起止偏移包含外侧两个引号字节。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 比较两个字段的值与引号标记（不比较偏移）。
func (c Cell) Equal(o Cell) bool { return c.Value == o.Value && c.Quoted == o.Quoted }
