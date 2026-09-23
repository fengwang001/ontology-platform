// Package cell 表示 CSV 单个字段值及其原文定位与引号标记。
package cell

// Cell 是一个字段的解析结果。
// Quoted 区分未引号空字段（"" 的值表示，Quoted=false）
// 与加引号的空字段（原文 ""，Quoted=true）。
// Start/End 为该字段在原始输入中的字节偏移：Start 指向字段首字节
// （引号字段指向开头引号），End 指向字段最后一个字节之后的位置
// （引号字段指向闭合引号之后）。
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// String 返回字段值的字符串形式。
func (c Cell) String() string { return string(c.Value) }

// Equal 逐字段比较两个 Cell，含值、引号标记与偏移。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && c.Start == o.Start && c.End == o.End &&
		string(c.Value) == string(o.Value)
}
