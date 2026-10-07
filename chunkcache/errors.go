package chunkcache

import "errors"

// ErrorKind 为错误的固定优先级分类。数值越小优先级越高。
type ErrorKind int

const (
	// KindInvalidParam 参数非法（优先级最高）。
	KindInvalidParam ErrorKind = iota
	// KindRangeNotSatisfiable 范围不可满足（对象已存在但范围越界/空后缀）。
	KindRangeNotSatisfiable
	// KindCapacity 容量不足：完成请求必须同时持有的切片数超过 C。
	KindCapacity
	// KindBackend 回源失败：源站接口返回错误。
	KindBackend
	// KindVersionOscillation 版本震荡：一次请求内版本第二次发生变化。
	KindVersionOscillation
)

// 原因细分常量，用于区分同一错误类别下的不同情形。
const (
	ReasonNegativeStart      = "start_negative"
	ReasonNegativeSuffix     = "suffix_negative"
	ReasonEndBeforeStart     = "end_before_start"
	ReasonEmptyKey           = "empty_key"
	ReasonNonPositiveS       = "non_positive_slice_size"
	ReasonNonPositiveC       = "non_positive_capacity"
	ReasonStartAtOrAfterSize = "start_at_or_after_length"
	ReasonZeroSuffix         = "zero_suffix"
)

// 固定优先级的哨兵错误，可用 errors.Is 判定类别。
var (
	ErrInvalidParam        = errors.New("chunkcache: invalid parameter")
	ErrRangeNotSatisfiable = errors.New("chunkcache: range not satisfiable")
	ErrCapacityExceeded    = errors.New("chunkcache: capacity exceeded")
	ErrBackend             = errors.New("chunkcache: backend fetch failed")
	ErrVersionOscillation  = errors.New("chunkcache: version oscillation")
)

// Error 是本包返回的结构化错误，携带类别、细分原因与底层错误。
type Error struct {
	Kind   ErrorKind
	Reason string
	Op     string
	Err    error
}

func (e *Error) Error() string {
	msg := "chunkcache: " + e.Op + ": "
	switch e.Kind {
	case KindInvalidParam:
		msg += "invalid parameter"
	case KindRangeNotSatisfiable:
		msg += "range not satisfiable"
	case KindCapacity:
		msg += "capacity exceeded"
	case KindBackend:
		msg += "backend fetch failed"
	case KindVersionOscillation:
		msg += "version oscillation"
	}
	if e.Reason != "" {
		msg += " (" + e.Reason + ")"
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *Error) Unwrap() error {
	switch e.Kind {
	case KindInvalidParam:
		return ErrInvalidParam
	case KindRangeNotSatisfiable:
		return ErrRangeNotSatisfiable
	case KindCapacity:
		return ErrCapacityExceeded
	case KindBackend:
		return ErrBackend
	case KindVersionOscillation:
		return ErrVersionOscillation
	}
	return nil
}

func newError(kind ErrorKind, op, reason string, err error) *Error {
	return &Error{Kind: kind, Op: op, Reason: reason, Err: err}
}
