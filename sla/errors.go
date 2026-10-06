package sla

// Code 是可程序化区分的错误类别。
type Code int

const (
	CodeOK Code = iota
	CodeInvalidParam
	CodeClockRollback
	CodeOrderNotFound
	CodeOrderCanceled
	CodeEventOrder
	CodeAlreadyReaddressed
	CodeAlreadyPaid
	CodeNotDelivered
	CodeWindowClosed
	CodeNoDelay
)

// Error 携带稳定的错误码，调用方可用 Code() 程序化区分。
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func errf(code Code, msg string) *Error { return &Error{Code: code, Msg: msg} }
