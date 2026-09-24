// Package cell 表示一个 CSV 字段值及其原文位置。
package cell

// Cell 是一个字段：值、是否带引号、原文起止字节偏移。
type Cell struct {
	Value  string
	Quoted bool
	Start  int // 起始字节偏移（含开引号），从 0 起
	End    int // 结束偏移（含闭引号），半开区间 [Start, End)
}
