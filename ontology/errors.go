package ontology

import "fmt"

// ErrorCode 是固定且唯一排序的错误类别。
// 数值越小优先级越高；一次调用同时命中多类错误时，只汇报优先级最高者。
type ErrorCode int

const (
	// ErrNotFound 租户或对象类型不存在。
	ErrNotFound ErrorCode = iota
	// ErrMissingBasis 覆盖规则声明的放宽缺少必需的授权依据。
	ErrMissingBasis
	// ErrOverrideConflict 归属租户的覆盖规则存在无法调和的内部冲突。
	ErrOverrideConflict
	// ErrMergeAmbiguous 覆盖粒度不一致导致合并结果不唯一。
	ErrMergeAmbiguous
)

// Error 是模块统一错误类型。
type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Msg)
}

func (c ErrorCode) String() string {
	switch c {
	case ErrNotFound:
		return "not-found"
	case ErrMissingBasis:
		return "missing-basis"
	case ErrOverrideConflict:
		return "override-conflict"
	case ErrMergeAmbiguous:
		return "merge-ambiguous"
	}
	return "unknown"
}

func newError(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}
