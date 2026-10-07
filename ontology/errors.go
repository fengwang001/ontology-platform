// Package ontology 提供对象生命周期状态机与校验钩子触发范围子系统。
//
// 本文件为错误归一化模块：把所有拒绝原因收敛为四类、保持固定优先级。
package ontology

// ErrorCode 是四类可相互区分的拒绝原因。
type ErrorCode string

const (
	// ErrorInvalidArgument 参数非法（实例不存在、目标阶段未声明），优先级最高。
	ErrorInvalidArgument ErrorCode = "invalid_argument"
	// ErrorTerminal 起始阶段为终态，优先级次之。
	ErrorTerminal ErrorCode = "terminal_state"
	// ErrorTransitionNotAllowed 转移关系未在声明的允许关系中，优先级再次之。
	ErrorTransitionNotAllowed ErrorCode = "transition_not_allowed"
	// ErrorHookFailed 校验钩子失败，优先级最低。
	ErrorHookFailed ErrorCode = "hook_failed"
)

// LifecycleError 是本子系统对外暴露的唯一错误形态。
type LifecycleError struct {
	Code   ErrorCode
	Reason string
	// HookID 仅在 Code == ErrorHookFailed 时有意义，标识首个失败的钩子。
	HookID string
}

func (e *LifecycleError) Error() string {
	if e.HookID != "" {
		return string(e.Code) + ": " + e.Reason + " (hook=" + e.HookID + ")"
	}
	return string(e.Code) + ": " + e.Reason
}

// newError 是各模块统一构造归一化错误的唯一入口。
func newError(code ErrorCode, reason string) *LifecycleError {
	return &LifecycleError{Code: code, Reason: reason}
}

// AsLifecycleError 提取归一化错误；非本子系统错误时返回 nil, false。
func AsLifecycleError(err error) (*LifecycleError, bool) {
	if err == nil {
		return nil, false
	}
	if le, ok := err.(*LifecycleError); ok {
		return le, true
	}
	return nil, false
}
