// Package exports 实现模块包导出映射（exports map）的解析器。
//
// 给定一张导出映射表与一次导入请求（子路径 + 活动条件集合），
// 决定请求最终落到哪个内部目标，或给出可区分的错误类别。
package exports

import "fmt"

// Kind 标识错误的类别，调用方可以据此区分不同的失败原因。
type Kind int

const (
	// KindInvalidTable 表示导出映射表在构造或替换时未通过校验。
	KindInvalidTable Kind = iota
	// KindInvalidRequest 表示请求本身非法（子路径形态错误或条件名为空）。
	KindInvalidRequest
	// KindSubpathNotExported 表示没有任何键能匹配请求的子路径。
	KindSubpathNotExported
	// KindForbidden 表示命中了显式禁止（null）目标。
	KindForbidden
	// KindNoMatchingCondition 表示条件映射中没有任何条件命中。
	KindNoMatchingCondition
	// KindInvalidTarget 表示解析得到的字符串目标不合法。
	KindInvalidTarget
)

func (k Kind) String() string {
	switch k {
	case KindInvalidTable:
		return "invalid table"
	case KindInvalidRequest:
		return "invalid request"
	case KindSubpathNotExported:
		return "subpath not exported"
	case KindForbidden:
		return "forbidden"
	case KindNoMatchingCondition:
		return "no matching condition"
	case KindInvalidTarget:
		return "invalid target"
	default:
		return "unknown error"
	}
}

// Error 是解析器返回的全部错误的统一类型，通过 Kind 字段区分类别。
type Error struct {
	Kind   Kind
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("exports: %s: %s", e.Kind, e.Detail)
}

// KindOf 从 err 中提取错误类别；err 不是 *Error 时返回 false。
func KindOf(err error) (Kind, bool) {
	if e, ok := err.(*Error); ok {
		return e.Kind, true
	}
	return 0, false
}

func errf(kind Kind, format string, args ...any) *Error {
	return &Error{Kind: kind, Detail: fmt.Sprintf(format, args...)}
}
