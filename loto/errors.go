package loto

import "fmt"

// Code 是可区分、且按约定严格排序的错误类别。
// 数值越小优先级越高：当一次调用同时存在多个问题时，必须返回编号最小的那一类。
type Code int

const (
	InvalidParam     Code = 1 // 参数非法
	ClockRollback    Code = 2 // 时刻回退
	NotFound         Code = 3 // 对象不存在
	PermissionDenied Code = 4 // 无权限或角色不符
	StateNotAllowed  Code = 5 // 状态不允许
	Conflict         Code = 6 // 票间冲突
	ConditionNotMet  Code = 7 // 条件不满足（身份、在场、未全部上锁等）
)

func (c Code) String() string {
	switch c {
	case InvalidParam:
		return "InvalidParam"
	case ClockRollback:
		return "ClockRollback"
	case NotFound:
		return "NotFound"
	case PermissionDenied:
		return "PermissionDenied"
	case StateNotAllowed:
		return "StateNotAllowed"
	case Conflict:
		return "Conflict"
	case ConditionNotMet:
		return "ConditionNotMet"
	default:
		return fmt.Sprintf("Code(%d)", int(c))
	}
}

// OpError 是所有被拒绝操作返回的错误。
type OpError struct {
	Code Code
	Msg  string
}

func (e *OpError) Error() string { return e.Code.String() + ": " + e.Msg }

func fail(code Code, format string, args ...any) error {
	return &OpError{Code: code, Msg: fmt.Sprintf(format, args...)}
}
