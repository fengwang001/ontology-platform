package rider

import "errors"

// ErrorCode 可程序化区分的拒绝原因。
type ErrorCode int

const (
	ErrInvalidConfig ErrorCode = iota + 1
	ErrInvalidArgument
	ErrClockSkew
	ErrRiderNotFound
	ErrEventNotFound
	ErrAppealNotFound
	ErrEventRevoked
	ErrAlreadyAppealed
	ErrAppealDecided
	ErrCompanionEvent
	ErrAppealWindowExpired
)

// OpError 携带可程序化判别的错误码。
type OpError struct {
	Code ErrorCode
	Msg  string
}

func (e *OpError) Error() string { return e.Msg }

func newError(code ErrorCode, msg string) error {
	return &OpError{Code: code, Msg: msg}
}

// CodeOf 返回错误对应的 ErrorCode；非本系统错误返回 0。
func CodeOf(err error) ErrorCode {
	var opErr *OpError
	if errors.As(err, &opErr) {
		return opErr.Code
	}
	return 0
}
