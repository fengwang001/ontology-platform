package ontology

// Kind 表示导入拒绝原因的三大类别。
// 只允许命中其中之一；报错优先级固定为
// KindParamInvalid -> KindPreHook -> KindPostHook。
type Kind string

const (
	// KindParamInvalid 参数非法：主键重复、字段类型不符。
	KindParamInvalid Kind = "param_invalid"
	// KindPreHook 单条记录的前置校验钩子失败。
	KindPreHook Kind = "pre_hook"
	// KindPostHook 批次级后置校验钩子失败（仅 ALL_OR_NOTHING）。
	KindPostHook Kind = "post_hook"
)

// HookError 是归一化后的拒绝原因。
// 三类错误通过 Kind 字段可相互区分。
type HookError struct {
	Kind    Kind
	Index   int    // 命中的记录下标（整批参数非法时为 -1）
	Code    string // 稳定的机器可读原因码
	Message string // 人类可读说明
}

func (e *HookError) Error() string {
	return string(e.Kind) + ": " + e.Code + " (" + e.Message + ")"
}

func paramError(index int, code, msg string) *HookError {
	return &HookError{Kind: KindParamInvalid, Index: index, Code: code, Message: msg}
}

func preError(index int, code, msg string) *HookError {
	return &HookError{Kind: KindPreHook, Index: index, Code: code, Message: msg}
}

func postError(code, msg string) *HookError {
	return &HookError{Kind: KindPostHook, Index: -1, Code: code, Message: msg}
}
