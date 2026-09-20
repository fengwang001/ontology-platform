// Package join 提供两表等值连接（Inner / Left），详见 doc.go 的语义说明。
package join

// Row 表中的一行，属性名到属性值的映射。
type Row map[string]any

// Mode 连接模式。
type Mode int

const (
	// Inner 只输出左右两侧键相等匹配上的行对。
	Inner Mode = iota
	// Left 在 Inner 的基础上，额外输出所有未匹配的左行。
	Left
)

// String 返回模式名，用于日志与错误信息。
func (m Mode) String() string {
	switch m {
	case Inner:
		return "inner"
	case Left:
		return "left"
	}
	return "unknown"
}

// Stats 一次连接的统计信息，全部由 Join 填充。
type Stats struct {
	// LeftRows / RightRows 为输入行数。
	LeftRows  int
	RightRows int

	// MatchedRows 为匹配展开产出的行数，等于逐键 m*n 之和。
	MatchedRows int
	// OutputRows 为最终结果行数；Inner 下等于 MatchedRows，
	// Left 下等于 MatchedRows + LeftNullKeyRows + LeftUnmatchedRows。
	OutputRows int
	// MaxKeyExpansion 为最大单键展开倍数，即所有键上 m*n 的最大值。
	MaxKeyExpansion int

	// LeftNullKeyRows 为因连接键为空（属性缺失、值为 nil 或 NaN）
	// 而永不匹配的左行数。
	LeftNullKeyRows int
	// LeftUnmatchedRows 为键有值但右表无对应行的左行数。
	// 与 LeftNullKeyRows 严格分开统计。
	LeftUnmatchedRows int
	// RightNullKeyRows 为键为空的右行数（这类右行同样永不匹配）。
	RightNullKeyRows int
}

// Result 一次连接的结果。
type Result struct {
	// Rows 为结果行，顺序完全确定（见 doc.go 的排序规则）。
	Rows []Row
	// Stats 为本次连接的统计信息。
	Stats Stats
}
