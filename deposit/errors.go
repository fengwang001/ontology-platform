package deposit

import (
	"fmt"
	"strconv"
)

// ErrCode 是押金服务对外报告的错误类别，按规范规定的固定次序排列。
type ErrCode int

const (
	// ErrOK 表示操作成功。
	ErrOK ErrCode = iota
	// ErrIllegalArgument 参数非法。
	ErrIllegalArgument
	// ErrClockRollback 时钟回退：now 小于上一次被接受操作的 now。
	ErrClockRollback
	// ErrNoLease 租约不存在或尚未退房。
	ErrNoLease
	// ErrLate 申报或争议逾期。
	ErrLate
	// ErrState 当前状态不允许该操作（重复争议、已裁定再裁定、已撤销、无可退金额等）。
	ErrState
	// ErrAmount 金额越界。
	ErrAmount
)

// Error 实现 error 接口。
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Code.String() + ": " + e.Msg
}

// String 返回错误码的简短名称。
func (c ErrCode) String() string {
	switch c {
	case ErrOK:
		return "OK"
	case ErrIllegalArgument:
		return "illegal argument"
	case ErrClockRollback:
		return "clock rollback"
	case ErrNoLease:
		return "lease not found or not checked out"
	case ErrLate:
		return "past deadline"
	case ErrState:
		return "operation not allowed in current state"
	case ErrAmount:
		return "amount out of range"
	default:
		return "unknown error(" + strconv.Itoa(int(c)) + ")"
	}
}

func errf(code ErrCode, format string, args ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}
