package lifecycle

import "fmt"

// ErrorCode 是被拒绝迁移的固定错误分类。多个条件同时成立时，
// 引擎始终报告优先级数值最小（最高优先级）的那一类。
type ErrorCode int

const (
	// ErrUndeclared 目标状态或迁移规则未声明（含当前状态不允许该迁移）。
	ErrUndeclared ErrorCode = iota + 1
	// ErrPrecondition 前置条件不成立（含属性校验钩子不通过）。
	ErrPrecondition
	// ErrMutex 互斥迁移被拒绝：同一处理单元内同实例同互斥组只能放行一条。
	ErrMutex
	// ErrCardinality 迁移后链接基数校验失败。
	ErrCardinality
	// ErrHook 跨实例联动钩子拒绝（被连接实例不处于链接类型要求的状态）。
	ErrHook
	// ErrCycle 链式触发形成循环。
	ErrCycle
	// ErrTerminal 终态实例的任何改动尝试（状态、属性、新增链接）。
	ErrTerminal
)

// Name 返回错误码的稳定名称。
func (c ErrorCode) Name() string {
	switch c {
	case ErrUndeclared:
		return "UNDECLARED"
	case ErrPrecondition:
		return "PRECONDITION_FAILED"
	case ErrMutex:
		return "MUTEX_REJECTED"
	case ErrCardinality:
		return "CARDINALITY_VIOLATION"
	case ErrHook:
		return "CROSS_INSTANCE_HOOK_REJECTED"
	case ErrCycle:
		return "CASCADE_CYCLE"
	case ErrTerminal:
		return "TERMINAL_PROTECTED"
	default:
		return "UNKNOWN"
	}
}

// LifecycleError 描述一次被拒绝操作的分类与可读细节。
type LifecycleError struct {
	Code   ErrorCode
	Target InstanceID
	Rule   string
	Detail string
}

func (e *LifecycleError) Error() string {
	return fmt.Sprintf("lifecycle: %s target=%s rule=%q: %s",
		e.Code.Name(), e.Target, e.Rule, e.Detail)
}

func newErr(code ErrorCode, target InstanceID, rule, detail string) *LifecycleError {
	return &LifecycleError{Code: code, Target: target, Rule: rule, Detail: detail}
}
