// Package cell 表示 CSV 的一个字段值及其原文位置与引号标记。
package cell

// Cell 是一个字段。Quoted 区分未引号空字段与 "" 加引号空字段。
// Off/End 为该字段（含引号）在原输入流中的字节偏移 [Off, End)。
type Cell struct {
	Value  string
	Quoted bool
	Off    int64
	End    int64
}

// Clone 返回独立副本。
func (c Cell) Clone() Cell { return c }
