// Package submission 实现作业提交截止、延期与迟交扣分结算引擎。
package submission

import "fmt"

// Code 是可区分的错误类别。声明顺序即固定优先级：数值越小越优先。
type Code int

const (
	ErrInvalidArgument           Code = iota // 参数非法
	ErrClockRegression                       // 时钟回退
	ErrNotFound                              // 作业/主体/版本不存在
	ErrAlreadySettled                        // 作业已结算
	ErrHardClosed                            // 已超过硬性关闭时刻
	ErrStateNotAllowed                       // 状态不允许（首交后加入小组、非成员操作小组等）
	ErrExtensionExceedsHardClose             // 延期超出硬性关闭
	ErrVersionInvalid                        // 指定的版本无效
)

func (c Code) String() string {
	switch c {
	case ErrInvalidArgument:
		return "invalid argument"
	case ErrClockRegression:
		return "clock regression"
	case ErrNotFound:
		return "not found"
	case ErrAlreadySettled:
		return "already settled"
	case ErrHardClosed:
		return "hard closed"
	case ErrStateNotAllowed:
		return "state not allowed"
	case ErrExtensionExceedsHardClose:
		return "extension exceeds hard close"
	case ErrVersionInvalid:
		return "version invalid"
	}
	return "unknown"
}

// Error 是引擎返回的唯一错误类型，Code 用于精确分类。
type Error struct {
	Code Code
	Op   string
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.Op, e.Code, e.Msg)
}

func newErr(op string, code Code, format string, args ...any) *Error {
	return &Error{Code: code, Op: op, Msg: fmt.Sprintf(format, args...)}
}
