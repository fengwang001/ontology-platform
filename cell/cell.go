// Package cell 表示 CSV 的一个字段值及其原文元数据。
package cell

// Cell 是一个字段。Quoted 区分未引号空字段与 "".
// Start/End 是该字段在原文中的字节偏移区间 [Start, End)。
// 未引号字段：Start 指向首字符（无字符时指向分隔符/行尾位置），
// End 指向结束分隔符位置；引号字段：Start 指向开引号，End 指向闭引号之后。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 报告两个字段逐位相等（含引号标记；偏移不参与，偏移另行比对）。
func (c Cell) Equal(o Cell) bool { return c.Value == o.Value && c.Quoted == o.Quoted }

// SamePos 报告偏移相同。
func (c Cell) SamePos(o Cell) bool { return c.Start == o.Start && c.End == o.End }
