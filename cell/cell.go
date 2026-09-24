// Package cell 表示一个字段值及其在原文中的位置。
package cell

// Field 是一个已解析字段。Quoted 区分未加引号的空字段与 ""。
// Start/End 是该字段在原文中的字节偏移（[Start, End)，含引号本身）。
type Field struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 逐位比较两个字段（值、引号标记、偏移）。
func (f Field) Equal(o Field) bool {
	return f.Value == o.Value && f.Quoted == o.Quoted && f.Start == o.Start && f.End == o.End
}
