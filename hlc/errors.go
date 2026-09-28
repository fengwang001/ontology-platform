package hlc

import "errors"

// 拒绝原因（哨兵错误，用 errors.Is 区分）。
var (
	ErrInvalidArgument    = errors.New("invalid argument")
	ErrNodeNotFound       = errors.New("node not found")
	ErrNegativePhysical   = errors.New("negative physical time")
	ErrMessageNotFound    = errors.New("message not found")
	ErrMessageReceived    = errors.New("message already received")
	ErrWrongRecipient     = errors.New("message target does not match receiving node")
	ErrOffsetExceeded     = errors.New("physical clock offset exceeded")
	ErrCounterOverflow    = errors.New("hlc counter overflow")
)

// Error 携带被拒操作的上下文与原因。
type Error struct {
	Op     string
	Reason error
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return "hlc: " + e.Op + ": " + e.Reason.Error()
	}
	return "hlc: " + e.Op + ": " + e.Reason.Error() + ": " + e.Detail
}
func (e *Error) Unwrap() error { return e.Reason }

func fail(op string, reason error, detail string) error {
	return &Error{Op: op, Reason: reason, Detail: detail}
}
