package calendar

import "fmt"

// ErrCode 是固定的错误类别。所有操作按如下固定次序检查，只报告第一个错误：
// 参数非法 -> 时钟回退 -> 不存在 -> 状态不允许 -> 日期不可订。
type ErrCode int

const (
	ErrInvalidParam  ErrCode = iota + 1 // 参数非法
	ErrClockRollback                    // 时钟回退
	ErrNotFound                         // 房源或预订不存在
	ErrInvalidState                     // 状态不允许该操作
	ErrNotBookable                      // 日期不可订
)

func (c ErrCode) String() string {
	switch c {
	case ErrInvalidParam:
		return "invalid-param"
	case ErrClockRollback:
		return "clock-rollback"
	case ErrNotFound:
		return "not-found"
	case ErrInvalidState:
		return "invalid-state"
	case ErrNotBookable:
		return "not-bookable"
	default:
		return "unknown"
	}
}

// Reason 进一步区分「日期不可订」的具体原因。
type Reason int

const (
	ReasonNone            Reason = iota
	ReasonBlocked                // 与封锁区间相交
	ReasonBookingConflict        // 与有效保留或已确认预订相交
	ReasonGapTooSmall            // 换客间隙不足
	ReasonMinStayTooShort        // 最短入住不足
	ReasonOrphanNight            // 产生孤夜
)

func (r Reason) String() string {
	switch r {
	case ReasonBlocked:
		return "blocked"
	case ReasonBookingConflict:
		return "booking-conflict"
	case ReasonGapTooSmall:
		return "gap-too-small"
	case ReasonMinStayTooShort:
		return "min-stay-too-short"
	case ReasonOrphanNight:
		return "orphan-night"
	default:
		return "none"
	}
}

// Error 是服务返回的唯一错误类型，携带类别与不可订子原因。
type Error struct {
	Code   ErrCode
	Reason Reason
	Msg    string
}

func (e *Error) Error() string {
	if e.Code == ErrNotBookable {
		return fmt.Sprintf("%s/%s: %s", e.Code, e.Reason, e.Msg)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Msg)
}

func errInvalidParam(format string, args ...any) *Error {
	return &Error{Code: ErrInvalidParam, Msg: fmt.Sprintf(format, args...)}
}

func errClockRollback(now, last int64) *Error {
	return &Error{Code: ErrClockRollback, Msg: fmt.Sprintf("now=%d 小于上一次被接受操作的 now=%d", now, last)}
}

func errNotFound(format string, args ...any) *Error {
	return &Error{Code: ErrNotFound, Msg: fmt.Sprintf(format, args...)}
}

func errInvalidState(format string, args ...any) *Error {
	return &Error{Code: ErrInvalidState, Msg: fmt.Sprintf(format, args...)}
}

func errNotBookable(reason Reason, format string, args ...any) *Error {
	return &Error{Code: ErrNotBookable, Reason: reason, Msg: fmt.Sprintf(format, args...)}
}
