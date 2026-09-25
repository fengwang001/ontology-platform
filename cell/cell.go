// Package cell 表示 CSV 字段值及其原文位置。
package cell

// Cell 是一个字段的解码结果。
type Cell struct {
	Value  string
	Quoted bool
	Start  int // 原文起始字节偏移（含开引号）
	End    int // 原文结束字节偏移（不含）
}

// Clone 深拷贝字段（Value 是字符串不可变，这里仅显式复制）。
func (c Cell) Clone() Cell { return c }
