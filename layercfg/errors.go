package layercfg

import (
	"errors"
	"fmt"
)

// ErrorKind 是本包所有可区分错误的类别。
// 数值越小优先级越高；一次操作同时存在多个问题时返回优先级最高者。
type ErrorKind int

const (
	// ErrInvalidArgument 参数非法（含层限定非法、变更自身非法）。
	ErrInvalidArgument ErrorKind = iota + 1
	// ErrVersionNotFound 指定的历史版本不存在。
	ErrVersionNotFound
	// ErrKeyNotRegistered 键尚未登记模式。
	ErrKeyNotRegistered
	// ErrTypeOrRange 值类型与模式不符，或整数超出取值范围。
	ErrTypeOrRange
	// ErrLockConflict 更窄层违反已有锁定，或发布时锁定下仍存在窄层写入。
	ErrLockConflict
	// ErrConflict 同一次发布内对同一层同一键的同类槽位存在多条冲突变更。
	ErrConflict
	// ErrRequiredMissing 发布/回滚后某个已存在实体的解析视图缺少必填键。
	ErrRequiredMissing
)

// Error 携带错误类别与可读说明，调用方可用 errors.As 提取 Kind 做分支。
type Error struct {
	Kind ErrorKind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func errorf(kind ErrorKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

func kindOf(err error) ErrorKind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return ErrInvalidArgument
}
