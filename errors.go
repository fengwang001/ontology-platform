package ontology

import (
	"fmt"
	"strings"
)

// ParamKind 描述单个参数校验问题的类别，调用方可据此分别判定。
type ParamKind string

const (
	ParamMissingRequired ParamKind = "missing_required"
	ParamTypeMismatch    ParamKind = "type_mismatch"
	ParamUnknown         ParamKind = "unknown_param"
)

// ParamError 是单个参数上的一个校验问题。
type ParamError struct {
	Name     string
	Kind     ParamKind
	Expected string
	Got      string
	Message  string
}

func (p ParamError) Error() string { return p.Message }

// ParamErrors 是一次调用中收集到的全部参数问题，
// 校验不会在第一个问题处提前返回。
type ParamErrors []ParamError

func (ps ParamErrors) Error() string {
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = p.Message
	}
	return "参数校验失败: " + strings.Join(parts, "; ")
}

// HookRejectError 表示某个前置钩子拒绝了 Action（索引从 1 开始）。
type HookRejectError struct {
	Action string
	Index  int
	Reason string
}

func (e *HookRejectError) Error() string {
	return fmt.Sprintf("action %q 的第 %d 个前置钩子拒绝执行: %s", e.Action, e.Index, e.Reason)
}

// HookWriteError 表示某个只读钩子尝试写入状态（索引从 1 开始）。
type HookWriteError struct {
	Action string
	Index  int
}

func (e *HookWriteError) Error() string {
	return fmt.Sprintf("action %q 的第 %d 个前置钩子越权写入，事务失败", e.Action, e.Index)
}

// DepthError 表示嵌套深度超过上限。
type DepthError struct {
	Chain []string
	Limit int
}

func (e *DepthError) Error() string {
	return fmt.Sprintf("嵌套深度超过上限 %d，调用链: %s", e.Limit, strings.Join(e.Chain, " -> "))
}

// RecursionError 表示同一 Action 在调用链中重复出现。
type RecursionError struct {
	Chain []string
}

func (e *RecursionError) Error() string {
	return "检测到递归调用，调用链: " + strings.Join(e.Chain, " -> ")
}

// InconsistentError 表示存储已因撤销失败进入不一致状态，拒绝写入。
type InconsistentError struct {
	Step string
}

func (e *InconsistentError) Error() string {
	return "存储不一致，拒绝写入；无法撤销的步骤: " + e.Step
}
