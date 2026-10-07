package ontology

import "fmt"

// ErrorClass 是系统明确区分的错误类别。
// 类别数值即固定且唯一的汇报优先顺序：数值小者优先汇报。
type ErrorClass int

const (
	// ErrNotFound 租户、对象类型、实例或属性不存在。
	ErrNotFound ErrorClass = iota + 1
	// ErrMissingBasis 覆盖规则声明的放宽缺少必需的授权依据。
	ErrMissingBasis
	// ErrOverrideConflict 归属租户的覆盖规则本身存在无法调和的内部冲突。
	ErrOverrideConflict
	// ErrMergeNonUnique 覆盖粒度不一致导致合并结果不唯一。
	ErrMergeNonUnique
)

func (c ErrorClass) String() string {
	switch c {
	case ErrNotFound:
		return "not-found"
	case ErrMissingBasis:
		return "missing-basis"
	case ErrOverrideConflict:
		return "override-conflict"
	case ErrMergeNonUnique:
		return "merge-non-unique"
	default:
		return "unknown"
	}
}

// Error 是模块返回的分类错误。
type Error struct {
	Class   ErrorClass
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Class, e.Message)
}

func newError(class ErrorClass, format string, args ...any) *Error {
	return &Error{Class: class, Message: fmt.Sprintf(format, args...)}
}

// ClassOf 返回错误的类别；非模块错误返回 0。
func ClassOf(err error) ErrorClass {
	if e, ok := err.(*Error); ok {
		return e.Class
	}
	return 0
}
