// Package dtc 实现车载诊断故障码（DTC）生命周期管理。
package dtc

import "fmt"

// ErrorKind 区分被拒绝操作的原因，检查次序固定为：
// 参数非法 > 时刻或里程回退 > 事件顺序非法 > 故障码未登记 > 状态不允许。
type ErrorKind int

const (
	ErrInvalidParam ErrorKind = iota
	ErrRegression
	ErrEventOrder
	ErrDTCNotRegistered
	ErrStateNotAllowed
)

func (k ErrorKind) String() string {
	switch k {
	case ErrInvalidParam:
		return "invalid param"
	case ErrRegression:
		return "time or odometer regression"
	case ErrEventOrder:
		return "illegal event order"
	case ErrDTCNotRegistered:
		return "dtc not registered"
	case ErrStateNotAllowed:
		return "state not allowed"
	}
	return "unknown error"
}

// Error 是管理器返回的可区分错误。
type Error struct {
	Kind   ErrorKind
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("dtc: %s: %s", e.Kind, e.Detail)
}

func newError(kind ErrorKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Detail: fmt.Sprintf(format, args...)}
}
