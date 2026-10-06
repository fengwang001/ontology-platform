package kitchen

import "fmt"

// ErrorCode 用枚举让各类错误可程序化区分；错误检查使用 errors.As。
type ErrorCode int

const (
	// ErrInvalidParam：参数非法（配置、时刻、制作时长、订单号等）。
	ErrInvalidParam ErrorCode = iota + 1
	// ErrClockBackward：被接受操作携带时刻早于此前被接受操作的最大时刻。
	ErrClockBackward
	// ErrOrderNotFound：操作引用的订单不存在。
	ErrOrderNotFound
	// ErrOrderExists：接单时订单号已存在。
	ErrOrderExists
	// ErrNotStarted：对未开工订单报告完成。
	ErrNotStarted
	// ErrAlreadyStarted：取消已开工订单。
	ErrAlreadyStarted
	// ErrAlreadyDone：操作已完成订单。
	ErrAlreadyDone
	// ErrMerchantPaused：商家暂停期间的新即时单/新预约单。
	ErrMerchantPaused
	// ErrReservationTooSoon：预约单目标开工时刻早于当前时刻加预约单提前量。
	ErrReservationTooSoon
	// ErrOverloaded：预计等待不小于爆单阈值。
	ErrOverloaded
)

// Error 是本包返回的唯一错误实体，携带错误码与可读信息。
type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("kitchen: %s: %s", codeName(e.Code), e.Msg)
}

func codeName(c ErrorCode) string {
	switch c {
	case ErrInvalidParam:
		return "invalid param"
	case ErrClockBackward:
		return "clock backward"
	case ErrOrderNotFound:
		return "order not found"
	case ErrOrderExists:
		return "order exists"
	case ErrNotStarted:
		return "order not started"
	case ErrAlreadyStarted:
		return "order already started"
	case ErrAlreadyDone:
		return "order already done"
	case ErrMerchantPaused:
		return "merchant paused"
	case ErrReservationTooSoon:
		return "reservation too soon"
	case ErrOverloaded:
		return "overloaded"
	default:
		return "unknown"
	}
}

func newError(code ErrorCode, format string, args ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Is 支持 errors.Is 的精确比较（按错误码）。
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}
