package toollife

import "errors"

// ErrorCode 可区分的错误类别，判定优先级见设计说明。
type ErrorCode int

const (
	ErrInvalidArgument ErrorCode = iota + 1 // 参数非法（最高优先级）
	ErrNotFound                             // 刀组或刀具不存在
	ErrConflict                             // 申请编号冲突（重复但内容不同）
	ErrState                                // 状态不允许
	ErrNoTool                               // 无刀可用（含已耗尽）
	ErrNoMargin                             // 暂无余量：所有刀被预占占满而非耗尽
)

// Error 携带错误码与可读信息。
type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string { return codeName(e.Code) + ": " + e.Msg }

func codeName(c ErrorCode) string {
	switch c {
	case ErrInvalidArgument:
		return "invalid argument"
	case ErrNotFound:
		return "not found"
	case ErrConflict:
		return "conflict"
	case ErrState:
		return "state not allowed"
	case ErrNoTool:
		return "no tool available"
	case ErrNoMargin:
		return "no margin"
	default:
		return "unknown error"
	}
}

func errInvalid(format string, a ...any) error {
	return &Error{Code: ErrInvalidArgument, Msg: sprintf(format, a...)}
}
func errNotFound(format string, a ...any) error {
	return &Error{Code: ErrNotFound, Msg: sprintf(format, a...)}
}
func errConflict(format string, a ...any) error {
	return &Error{Code: ErrConflict, Msg: sprintf(format, a...)}
}
func errState(format string, a ...any) error {
	return &Error{Code: ErrState, Msg: sprintf(format, a...)}
}

// CodeOf 提取错误码；nil 返回 0。
func CodeOf(err error) ErrorCode {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}
