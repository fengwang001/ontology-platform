package ontology

import (
	"errors"
	"fmt"
)

// ErrorKind 区分转移被拒绝的四类原因。
// 判定优先级（只报第一个命中的原因）与常量声明顺序一致：
// 参数非法 < 起始阶段为终态 < 转移关系未允许 < 钩子校验失败。
type ErrorKind int

const (
	// ErrInvalidArgument 实例不存在或目标阶段未声明。
	ErrInvalidArgument ErrorKind = iota
	// ErrTerminalStage 起始阶段是终态，不允许任何转出（含转到自身）。
	ErrTerminalStage
	// ErrTransitionNotAllowed 转移关系未在对象类型的允许声明中。
	ErrTransitionNotAllowed
	// ErrHookFailed 钩子校验失败，转移整体不生效。
	ErrHookFailed
)

func (k ErrorKind) String() string {
	switch k {
	case ErrInvalidArgument:
		return "invalid_argument"
	case ErrTerminalStage:
		return "terminal_stage"
	case ErrTransitionNotAllowed:
		return "transition_not_allowed"
	case ErrHookFailed:
		return "hook_failed"
	default:
		return "unknown"
	}
}

// Error 是归一化后的转移错误，携带类别与上下文，可用 errors.As 提取。
type Error struct {
	Kind       ErrorKind
	ObjectType string
	InstanceID string
	From       Stage
	To         Stage
	Hook       string // 仅 ErrHookFailed：失败的钩子名
	Cause      error  // 仅 ErrHookFailed：钩子返回的原始错误
}

func (e *Error) Error() string {
	base := fmt.Sprintf("ontology: %s (type=%q instance=%q from=%q to=%q)",
		e.Kind, e.ObjectType, e.InstanceID, e.From, e.To)
	if e.Kind == ErrHookFailed {
		return fmt.Sprintf("%s hook=%q: %v", base, e.Hook, e.Cause)
	}
	return base
}

func (e *Error) Unwrap() error { return e.Cause }

// KindOf 从任意错误中提取归一化类别；非 *Error 错误返回 ok=false。
func KindOf(err error) (kind ErrorKind, ok bool) {
	var terr *Error
	if errors.As(err, &terr) {
		return terr.Kind, true
	}
	return 0, false
}

func newError(kind ErrorKind, t *ObjectType, inst *Instance, from, to Stage) *Error {
	e := &Error{Kind: kind, From: from, To: to}
	if t != nil {
		e.ObjectType = t.name
	}
	if inst != nil {
		e.InstanceID = inst.id
	}
	return e
}
