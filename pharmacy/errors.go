package pharmacy

import "fmt"

// ErrCode 区分各类错误，优先级即声明顺序（靠前者优先报告）。
type ErrCode int

const (
	ErrNone                ErrCode = iota
	ErrInvalidParam                // 参数非法
	ErrClockRollback               // 时钟回退
	ErrNotFound                    // 对象不存在
	ErrStateMismatch               // 状态不符
	ErrPrescriptionExpired         // 处方过期
	ErrOutOfStock                  // 缺药
	ErrNoValidReservation          // 无有效预留
)

func (c ErrCode) String() string {
	switch c {
	case ErrNone:
		return "无错误"
	case ErrInvalidParam:
		return "参数非法"
	case ErrClockRollback:
		return "时钟回退"
	case ErrNotFound:
		return "对象不存在"
	case ErrStateMismatch:
		return "状态不符"
	case ErrPrescriptionExpired:
		return "处方过期"
	case ErrOutOfStock:
		return "缺药"
	case ErrNoValidReservation:
		return "无有效预留"
	}
	return "未知错误"
}

// Error 是系统返回的唯一错误类型，Code 可用于程序化区分。
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Msg)
}

func newError(code ErrCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// NewError 供外部模型（如对照用朴素实现）构造同类型错误。
func NewError(code ErrCode, msg string) *Error {
	return &Error{Code: code, Msg: msg}
}

// CodeOf 提取错误的 ErrCode；err 为 nil 时返回 ErrNone。
func CodeOf(err error) ErrCode {
	if err == nil {
		return ErrNone
	}
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return ErrNone
}
