package medschedule

// ErrCode 按题目给定的错误优先级排列，数值越小优先级越高。
// 仅报第一个错误：
// 参数非法、时钟回退、对象不存在、状态不符、过敏冲突、
// 无对应计划点、间隔不足、次数超限、补给不允许。
type ErrCode int

const (
	ErrInvalidParam     ErrCode = iota // 参数非法
	ErrClockRollback                   // 时钟回退
	ErrNotFound                        // 对象不存在
	ErrBadState                        // 状态不符
	ErrAllergy                         // 过敏冲突
	ErrNoScheduledPoint                // 无对应计划点
	ErrIntervalTooShort                // 间隔不足
	ErrPRNLimit                        // 次数超限
	ErrMakeupNotAllowed                // 补给不允许
)

// Error 是带错误码的可区分错误。
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return e.Code.String() + ": " + e.Msg }

func (c ErrCode) String() string {
	switch c {
	case ErrInvalidParam:
		return "INVALID_PARAM"
	case ErrClockRollback:
		return "CLOCK_ROLLBACK"
	case ErrNotFound:
		return "NOT_FOUND"
	case ErrBadState:
		return "BAD_STATE"
	case ErrAllergy:
		return "ALLERGY_CONFLICT"
	case ErrNoScheduledPoint:
		return "NO_SCHEDULED_POINT"
	case ErrIntervalTooShort:
		return "INTERVAL_TOO_SHORT"
	case ErrPRNLimit:
		return "PRN_LIMIT_EXCEEDED"
	case ErrMakeupNotAllowed:
		return "MAKEUP_NOT_ALLOWED"
	}
	return "UNKNOWN"
}

func errInvalid(msg string) error  { return &Error{ErrInvalidParam, msg} }
func errRollback(msg string) error { return &Error{ErrClockRollback, msg} }
func errNotFound(msg string) error { return &Error{ErrNotFound, msg} }
func errBadState(msg string) error { return &Error{ErrBadState, msg} }
func errAllergy(msg string) error  { return &Error{ErrAllergy, msg} }
func errNoPoint(msg string) error  { return &Error{ErrNoScheduledPoint, msg} }
func errInterval(msg string) error { return &Error{ErrIntervalTooShort, msg} }
func errPRNLimit(msg string) error { return &Error{ErrPRNLimit, msg} }
func errMakeup(msg string) error   { return &Error{ErrMakeupNotAllowed, msg} }
