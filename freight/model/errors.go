package model

// Code 业务错误码，调用方据此区分拒绝原因。
type Code string

const (
	CodeInvalidArgument Code = "参数非法"
	CodeNoContract      Code = "无合同"
	CodeTimeNotCovered  Code = "时刻未覆盖"
	CodeOutOfRange      Code = "超出承运范围"
	CodeIntervalOverlap Code = "区间重叠"
	CodeAlreadySettled  Code = "已结算"
)

// Error 携带稳定错误码的业务错误。
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Msg }

// CodeOf 返回错误的业务错误码；非业务错误返回 CodeInvalidArgument 之外的空串。
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	if be, ok := err.(*Error); ok {
		return be.Code
	}
	return ""
}

func errf(code Code, format string, args ...any) error {
	return &Error{Code: code, Msg: sprintf(format, args...)}
}

// ErrIntervalOverlap 构造区间重叠错误。
func ErrIntervalOverlap(format string, args ...any) error {
	return errf(CodeIntervalOverlap, format, args...)
}

// ErrInvalid 构造参数非法错误。
func ErrInvalid(format string, args ...any) error {
	return errf(CodeInvalidArgument, format, args...)
}

// NewError 按错误码构造业务错误。
func NewError(code Code, format string, args ...any) error {
	return errf(code, format, args...)
}
