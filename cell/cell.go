// Package cell 表示一个 CSV 字段：值、是否加过引号、以及原文字节区间。
package cell

// Cell 记录字段的解析值与其在原始输入中的定位。
//
// Start/End 为半开字节区间 [Start,End)，覆盖字段在原文中的完整词法形态：
// 引号字段含两端引号；End 指向逗号/换行字节（不含）。
// Quoted 区分裸空字段（Quoted=false）与 ""（Quoted=true）。
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// Equal 比较两字段的值与引号标记（偏移不参与，供语义相等判定）。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && string(c.Value) == string(o.Value)
}
