package cedu

import "fmt"

// ErrorCode 区分所有可预期的拒绝原因。
type ErrorCode int

const (
	ErrInvalidParam ErrorCode = iota + 1
	ErrClockRollback
	ErrNotFound
	ErrStateNotAllowed
	ErrDuplicate
	ErrCorrectionExpired
	ErrCertificateExpired
)

func (c ErrorCode) String() string {
	switch c {
	case 0:
		return "OK"
	case ErrInvalidParam:
		return "INVALID_PARAM"
	case ErrClockRollback:
		return "CLOCK_ROLLBACK"
	case ErrNotFound:
		return "NOT_FOUND"
	case ErrStateNotAllowed:
		return "STATE_NOT_ALLOWED"
	case ErrDuplicate:
		return "DUPLICATE_REGISTRATION"
	case ErrCorrectionExpired:
		return "CORRECTION_WINDOW_EXPIRED"
	case ErrCertificateExpired:
		return "CERTIFICATE_EXPIRED"
	default:
		return fmt.Sprintf("UNKNOWN_ERROR(%d)", int(c))
	}
}

// Error 携带可区分的错误码，优先级仅由校验顺序保证。
type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string {
	return e.Code.String() + ": " + e.Msg
}

func newError(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}
