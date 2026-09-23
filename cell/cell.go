// Package cell 表示一个 CSV 字段的解析结果及其在原文中的字节位置。
package cell

// Cell 是一个字段。Quoted 区分未引号空字段（"" 的语义空值但未加引号）
// 与加引号空字段（原文为 ""，Quoted==true）。
// Start/End 为该字段在输入流中的字节偏移区间 [Start, End)，
// 其中引号字段包含外层引号，转义对 "" 计两个字节。
type Cell struct {
	Value  string
	Quoted bool
	Start  int64
	End    int64
}

// Empty 报告字段原文是否为空（未引号空字段）。
func (c Cell) Empty() bool { return !c.Quoted && len(c.Value) == 0 }

// Span 返回字段覆盖的字节数。
func (c Cell) Span() int64 { return c.End - c.Start }
