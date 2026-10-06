package review

// ErrorCode 用于区分所有可报告的错误类别。
type ErrorCode int

const (
	ErrInvalidParam          ErrorCode = iota + 1 // 参数非法
	ErrClockRollback                              // 时钟回退
	ErrNotFound                                   // 申报人或评审不存在
	ErrIllegalState                               // 状态不允许
	ErrNoPermission                               // 无权限
	ErrInsufficientReviewers                      // 评委不足
	ErrDuplicateVote                              // 重复投票
)

// Error 携带错误类别，便于调用方精确判别。
type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return codeName(e.Code) + ": " + e.Msg
}

func codeName(c ErrorCode) string {
	switch c {
	case ErrInvalidParam:
		return "参数非法"
	case ErrClockRollback:
		return "时钟回退"
	case ErrNotFound:
		return "申报人或评审不存在"
	case ErrIllegalState:
		return "状态不允许"
	case ErrNoPermission:
		return "无权限"
	case ErrInsufficientReviewers:
		return "评委不足"
	case ErrDuplicateVote:
		return "重复投票"
	default:
		return "未知错误"
	}
}

func errInvalid(format string, args ...any) error {
	return &Error{Code: ErrInvalidParam, Msg: sprintf(format, args...)}
}
func errClock(format string, args ...any) error {
	return &Error{Code: ErrClockRollback, Msg: sprintf(format, args...)}
}
func errNotFound(format string, args ...any) error {
	return &Error{Code: ErrNotFound, Msg: sprintf(format, args...)}
}
func errState(format string, args ...any) error {
	return &Error{Code: ErrIllegalState, Msg: sprintf(format, args...)}
}
func errPerm(format string, args ...any) error {
	return &Error{Code: ErrNoPermission, Msg: sprintf(format, args...)}
}
func errShort(format string, args ...any) error {
	return &Error{Code: ErrInsufficientReviewers, Msg: sprintf(format, args...)}
}
func errDup(format string, args ...any) error {
	return &Error{Code: ErrDuplicateVote, Msg: sprintf(format, args...)}
}
