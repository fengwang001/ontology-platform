package approval

import (
	"errors"
	"fmt"
)

// ErrKind 错误类别。各类别按声明顺序即规格要求的判定优先级：
// 参数非法 < 时钟回退 < 许可或环节不存在 < 状态不允许 < 无权限 <
// 前置环节未通过 < 补正次数超限。一个操作同时违反多条时只报优先级最高者。
type ErrKind int

const (
	ErrInvalidParam ErrKind = iota
	ErrClockRollback
	ErrNotFound
	ErrStateNotAllowed
	ErrNoPermission
	ErrPredecessorNotPassed
	ErrCorrectionLimitExceeded
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidParam:
		return "参数非法"
	case ErrClockRollback:
		return "时钟回退"
	case ErrNotFound:
		return "许可或环节不存在"
	case ErrStateNotAllowed:
		return "状态不允许"
	case ErrNoPermission:
		return "无权限"
	case ErrPredecessorNotPassed:
		return "前置环节未通过"
	case ErrCorrectionLimitExceeded:
		return "补正次数超限"
	}
	return "未知错误"
}

// Error 服务返回的唯一错误类型，携带类别与说明。
type Error struct {
	Kind ErrKind
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Kind, e.Msg)
}

// KindOf 取出错误的类别；非本服务错误时 ok=false。
func KindOf(err error) (kind ErrKind, ok bool) {
	var ae *Error
	if errors.As(err, &ae) {
		return ae.Kind, true
	}
	return 0, false
}

func invalidf(format string, args ...any) *Error { return &Error{ErrInvalidParam, fmt.Sprintf(format, args...)} }
func clockf(format string, args ...any) *Error   { return &Error{ErrClockRollback, fmt.Sprintf(format, args...)} }
func notFoundf(format string, args ...any) *Error {
	return &Error{ErrNotFound, fmt.Sprintf(format, args...)}
}
func statef(format string, args ...any) *Error {
	return &Error{ErrStateNotAllowed, fmt.Sprintf(format, args...)}
}
func noPermf(format string, args ...any) *Error {
	return &Error{ErrNoPermission, fmt.Sprintf(format, args...)}
}
func predNotPassedf(format string, args ...any) *Error {
	return &Error{ErrPredecessorNotPassed, fmt.Sprintf(format, args...)}
}
func corrLimitf(format string, args ...any) *Error {
	return &Error{ErrCorrectionLimitExceeded, fmt.Sprintf(format, args...)}
}
