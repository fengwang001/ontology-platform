package staffing

import "fmt"

// Code 是错误类别。数值越小优先级越高，判定时只返回第一个命中的类别。
type Code int

const (
	CodeOK Code = iota
	// CodeInvalidParam 参数非法。
	CodeInvalidParam
	// CodeClockRollback 时钟回退：now 小于上一次被接受操作的 now。
	CodeClockRollback
	// CodeNotFound 岗位、候选人或通知不存在。
	CodeNotFound
	// CodeInvalidState 状态不允许。
	CodeInvalidState
	// CodeFrozen 岗位冻结。
	CodeFrozen
	// CodeHeadcountFull 编制已满。
	CodeHeadcountFull
	// CodeBandExceeded 薪资超出职级带宽且无可用例外审批。
	CodeBandExceeded
	// CodePendingExists 候选人已有未决通知。
	CodePendingExists
	// CodeCooldown 候选人在同岗位的拒绝/放弃冷却期内。
	CodeCooldown
	// CodeExpired 已过期。
	CodeExpired
)

func (c Code) String() string {
	switch c {
	case CodeOK:
		return "OK"
	case CodeInvalidParam:
		return "INVALID_PARAM"
	case CodeClockRollback:
		return "CLOCK_ROLLBACK"
	case CodeNotFound:
		return "NOT_FOUND"
	case CodeInvalidState:
		return "INVALID_STATE"
	case CodeFrozen:
		return "FROZEN"
	case CodeHeadcountFull:
		return "HEADCOUNT_FULL"
	case CodeBandExceeded:
		return "BAND_EXCEEDED"
	case CodePendingExists:
		return "PENDING_EXISTS"
	case CodeCooldown:
		return "COOLDOWN"
	case CodeExpired:
		return "EXPIRED"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", int(c))
	}
}

// Error 携带类别、说明与（批量场景）失败下标。
type Error struct {
	Code  Code
	Msg   string
	Index int
}

func (e *Error) Error() string {
	if e.Index >= 0 {
		return fmt.Sprintf("%s: %s (batch index %d)", e.Code, e.Msg, e.Index)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Msg)
}

// ErrCode 从 error 中提取 Code；nil 返回 CodeOK。
func ErrCode(err error) Code {
	if err == nil {
		return CodeOK
	}
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return CodeOK
}

func newError(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...), Index: -1}
}

func newBatchError(code Code, index int, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...), Index: index}
}
