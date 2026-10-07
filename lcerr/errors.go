package lcerr

import (
	"errors"
	"fmt"
)

// Kind 是归一化后的转移拒绝类别。
type Kind int

const (
	// KindInvalidArgument 参数非法：实例不存在、目标阶段未声明等。
	KindInvalidArgument Kind = iota + 1
	// KindTerminalSource 起始阶段为终态，不允许任何转出（含转到自身）。
	KindTerminalSource
	// KindTransitionNotAllowed 转移关系未在对象类型中声明。
	KindTransitionNotAllowed
	// KindHookFailed 钩子校验失败。
	KindHookFailed
)

// String 返回类别的稳定字符串表示，用于日志与测试断言。
func (k Kind) String() string {
	switch k {
	case KindInvalidArgument:
		return "invalid_argument"
	case KindTerminalSource:
		return "terminal_source"
	case KindTransitionNotAllowed:
		return "transition_not_allowed"
	case KindHookFailed:
		return "hook_failed"
	default:
		return "unknown"
	}
}

// Error 是归一化后的错误。Err 字段保留底层原因（可为 nil）。
type Error struct {
	Kind Kind
	Op   string
	Msg  string
	Err  error
}

// New 构造一个无底层原因的归一化错误。
func New(kind Kind, op, msg string) *Error {
	return &Error{Kind: kind, Op: op, Msg: msg}
}

// Wrap 构造一个携带底层原因的归一化错误。
func Wrap(kind Kind, op, msg string, cause error) *Error {
	return &Error{Kind: kind, Op: op, Msg: msg, Err: cause}
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %s: %v", e.Op, e.Kind, e.Msg, e.Err)
	}
	return fmt.Sprintf("%s: %s: %s", e.Op, e.Kind, e.Msg)
}

// Unwrap 暴露底层原因，供 errors.Is / errors.As 使用。
func (e *Error) Unwrap() error { return e.Err }

// KindOf 提取错误的归一化类别；err 不是归一化错误时 ok 为 false。
func KindOf(err error) (kind Kind, ok bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind, true
	}
	return 0, false
}

// IsKind 判定 err 是否属于指定类别。
func IsKind(err error, kind Kind) bool {
	k, ok := KindOf(err)
	return ok && k == kind
}
