package transfer

import "fmt"

type ErrorCode int

const (
	ErrInvalidArgument ErrorCode = iota
	ErrClockRollback
	ErrTransferNotFound
	ErrInvalidState
	ErrInsufficientStock
	ErrOverReceipt
	ErrCloseTooEarly
	ErrRecoveryExceed
	ErrNoShortage
)

type Error struct {
	Code ErrorCode
	// State 仅在 Code == ErrInvalidState 时有效，用于区分单据当前状态。
	State Status
	Msg   string
}

func (e *Error) Error() string { return e.Msg }

// IsCode 判断错误是否为指定业务错误码；nil 与非 *Error 均返回 false。
func IsCode(err error, code ErrorCode) bool {
	if e, ok := err.(*Error); ok && e.Code == code {
		return true
	}
	return false
}

// CodeOf 返回错误码，nil 返回 -1。
func CodeOf(err error) ErrorCode {
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return -1
}

func fail(code ErrorCode, format string, args ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

func failState(st Status, action string) error {
	return &Error{
		Code:  ErrInvalidState,
		State: st,
		Msg:   fmt.Sprintf("invalid state: cannot %s in status %s", action, st),
	}
}
