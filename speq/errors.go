package speq

import "fmt"

// ErrorCode 错误码严格按以下次序区分（数字越小优先级越高）。
type ErrorCode int

const (
	// ErrInvalidParameter 参数非法。
	ErrInvalidParameter ErrorCode = iota
	// ErrDateRegression 日期回退（操作日期早于上一个被接受操作的日期）。
	ErrDateRegression
	// ErrNotFound 对象不存在。
	ErrNotFound
	// ErrScrapped 对象已报废。
	ErrScrapped
	// ErrIllegalState 状态不允许（如编号种类不符、封存对象被挂接）。
	ErrIllegalState
	// ErrConditionNotMet 条件不满足（如启封保障天数不足、转移附件不合格）。
	ErrConditionNotMet
)

func (c ErrorCode) String() string {
	switch c {
	case ErrInvalidParameter:
		return "参数非法"
	case ErrDateRegression:
		return "日期回退"
	case ErrNotFound:
		return "对象不存在"
	case ErrScrapped:
		return "对象已报废"
	case ErrIllegalState:
		return "状态不允许"
	case ErrConditionNotMet:
		return "条件不满足"
	default:
		return "未知错误"
	}
}

// OpError 带错误码的操作错误。
type OpError struct {
	Code ErrorCode
	Msg  string
}

func (e *OpError) Error() string {
	return e.Code.String() + ": " + e.Msg
}

func errf(code ErrorCode, format string, args ...any) error {
	return &OpError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// RejectReason 使用登记被拒绝时的首个不满足原因。
type RejectReason int

const (
	RejectNone RejectReason = iota
	RejectDeviceSealed
	RejectDeviceDisabled
	RejectDeviceExpired
	RejectNoSafetyValve
	RejectAttachment
)

func (r RejectReason) String() string {
	switch r {
	case RejectDeviceSealed:
		return "设备自身封存"
	case RejectDeviceDisabled:
		return "设备自身停用"
	case RejectDeviceExpired:
		return "设备自身超期"
	case RejectNoSafetyValve:
		return "缺少安全阀"
	case RejectAttachment:
		return "附件不满足"
	default:
		return "可使用"
	}
}

// RejectError 使用登记拒绝错误，携带原因与（若为附件原因）编号最小的附件。
type RejectError struct {
	Reason     RejectReason
	Attachment string
	Detail     string
}

func (e *RejectError) Error() string {
	if e.Reason == RejectAttachment {
		return e.Reason.String() + ": 附件 " + e.Attachment + " " + e.Detail
	}
	return e.Reason.String()
}
