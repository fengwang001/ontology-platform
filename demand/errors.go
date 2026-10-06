package demand

import "fmt"

// ErrKind 为可区分的错误类别。
type ErrKind int

const (
	// ErrInvalidParameter 参数非法（优先级最高）。
	ErrInvalidParameter ErrKind = iota + 1
	// ErrInvalidData 数据非法（上报内容不合法）。
	ErrInvalidData
	// ErrTimeRewind 时刻回退。
	ErrTimeRewind
	// ErrLoadNotFound 负荷不存在（运维操作）。
	ErrLoadNotFound
	// ErrStateNotAllowed 状态不允许（运维操作）。
	ErrStateNotAllowed
)

// Error 携带错误类别，便于调用方按类别处理。
type Error struct {
	Kind ErrKind
	Msg  string
}

func (e *Error) Error() string { return e.Kind.String() + ": " + e.Msg }

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidParameter:
		return "参数非法"
	case ErrInvalidData:
		return "数据非法"
	case ErrTimeRewind:
		return "时刻回退"
	case ErrLoadNotFound:
		return "负荷不存在"
	case ErrStateNotAllowed:
		return "状态不允许"
	default:
		return "未知错误"
	}
}

func errParam(format string, args ...any) error {
	return &Error{Kind: ErrInvalidParameter, Msg: fmt.Sprintf(format, args...)}
}
func errData(format string, args ...any) error {
	return &Error{Kind: ErrInvalidData, Msg: fmt.Sprintf(format, args...)}
}
func errRewind(format string, args ...any) error {
	return &Error{Kind: ErrTimeRewind, Msg: fmt.Sprintf(format, args...)}
}
func errNotFound(format string, args ...any) error {
	return &Error{Kind: ErrLoadNotFound, Msg: fmt.Sprintf(format, args...)}
}
func errState(format string, args ...any) error {
	return &Error{Kind: ErrStateNotAllowed, Msg: fmt.Sprintf(format, args...)}
}
