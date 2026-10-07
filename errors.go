package ontology

import (
	"errors"
	"fmt"
)

// ErrKind 分类所有可区分的拒绝原因。声明顺序即固定优先级：
// 数值越小优先级越高。一次操作同时命中多个错误时，报告优先级最高者。
type ErrKind int

const (
	ErrInvalidArgument       ErrKind = iota // 参数非法
	ErrClockRegression                      // 时钟回退
	ErrNotFound                             // 作业或主体或版本不存在
	ErrAlreadySettled                       // 作业已结算
	ErrClosed                               // 已超过硬性关闭时刻
	ErrStateNotAllowed                      // 状态不允许（首次提交后加入小组、非成员操作小组等）
	ErrExtensionExceedsClose                // 延期超出硬性关闭
	ErrVersionInvalid                       // 指定的版本无效
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidArgument:
		return "invalid argument"
	case ErrClockRegression:
		return "clock regression"
	case ErrNotFound:
		return "not found"
	case ErrAlreadySettled:
		return "already settled"
	case ErrClosed:
		return "past hard close"
	case ErrStateNotAllowed:
		return "state not allowed"
	case ErrExtensionExceedsClose:
		return "extension exceeds hard close"
	case ErrVersionInvalid:
		return "version invalid"
	}
	return "unknown"
}

// Error 是引擎返回的唯一错误类型，Kind 用于可区分的错误分类。
type Error struct {
	Op   string
	Kind ErrKind
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.Op, e.Kind, e.Msg)
}

func newErr(op string, kind ErrKind, format string, args ...any) *Error {
	return &Error{Op: op, Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// ErrKindOf 提取错误的分类；非引擎错误返回 false。
func ErrKindOf(err error) (ErrKind, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind, true
	}
	return 0, false
}
