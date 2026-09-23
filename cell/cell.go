// Package cell 表示 CSV 字段值：内容、引号标记、原文字节偏移。
package cell

// Cell 是一个已解析完成的字段。
// Quoted 区分「未加引号的空字段」与「加了引号的空字段 ""」。
// Start/End 为该字段在原始输入中的字节偏移，End 为开区间结束，
// 引号字段的区间包含两侧引号；空字段为零宽区间。
type Cell struct {
	Value  string
	Quoted bool
	Start  int64
	End    int64
}

// Limits 为解析上限，零值表示不限。
type Limits struct {
	MaxFieldBytes int64 // 单字段语义字节数上限（"" 计 1 字节）
	MaxFields     int64 // 单记录字段数上限
	MaxRecords    int64 // 总记录数上限
}
