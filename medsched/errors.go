package medsched

import "fmt"

// ErrorCode 标识一类可区分的业务错误。
type ErrorCode int

const (
	ErrInvalidParam     ErrorCode = iota + 1 // 参数非法（标识空、数值越界等）
	ErrClockRollback                         // 时钟回退
	ErrNotFound                              // 对象不存在
	ErrInvalidState                          // 状态不符
	ErrAllergy                               // 过敏冲突
	ErrNoScheduledPoint                      // 无对应计划点
	ErrIntervalTooShort                      // 间隔不足
	ErrLimitExceeded                         // 次数超限
	ErrMakeupNotAllowed                      // 补给不允许
)

func (c ErrorCode) String() string {
	switch c {
	case ErrInvalidParam:
		return "参数非法"
	case ErrClockRollback:
		return "时钟回退"
	case ErrNotFound:
		return "对象不存在"
	case ErrInvalidState:
		return "状态不符"
	case ErrAllergy:
		return "过敏冲突"
	case ErrNoScheduledPoint:
		return "无对应计划点"
	case ErrIntervalTooShort:
		return "间隔不足"
	case ErrLimitExceeded:
		return "次数超限"
	case ErrMakeupNotAllowed:
		return "补给不允许"
	default:
		return "未知错误"
	}
}

// Error 携带错误码，便于上层精确分支处理。
type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string { return e.Code.String() + ": " + e.Msg }

func errf(code ErrorCode, format string, args ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}
