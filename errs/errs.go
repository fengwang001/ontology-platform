// Package errs 负责错误归一化：动作调用被拒绝时，
// 所有内部失败都被归一化为三种可相互区分的类别，
// 并按固定优先级只报告第一个命中的原因。
package errs

import (
	"errors"
	"strconv"
	"strings"
)

// Kind 是归一化后的错误类别。
type Kind int

const (
	// KindInvalidArgument 参数非法：目标实例不存在、写入内容类型不符等。
	// 优先级最高，在前置钩子失败之前报告。
	KindInvalidArgument Kind = iota
	// KindPreHook 前置校验钩子失败，立即中止并整体回退。
	KindPreHook
	// KindPostHook 事务提交阶段后置校验钩子失败（聚合形式），优先级最低。
	KindPostHook
)

func (k Kind) String() string {
	switch k {
	case KindInvalidArgument:
		return "invalid-argument"
	case KindPreHook:
		return "pre-hook-failed"
	case KindPostHook:
		return "post-hook-failed"
	}
	return "unknown"
}

// Error 是归一化后的单点错误（参数非法或前置钩子失败）。
type Error struct {
	Kind       Kind     // 归一化类别
	ActionPath []string // 发生时的动作调用路径（外层在前）
	ObjectType string   // 涉及的对象类型（可为空）
	ObjectID   string   // 涉及的实例 ID（可为空）
	HookName   string   // 钩子失败时的钩子名（可为空）
	Message    string   // 人类可读的原因描述
}

// Error 实现 error 接口。
func (e *Error) Error() string {
	loc := ""
	if e.ObjectType != "" {
		loc = " [" + e.ObjectType + "/" + e.ObjectID + "]"
	}
	hook := ""
	if e.HookName != "" {
		hook = " hook=" + e.HookName
	}
	return e.Kind.String() + ": " + e.Message + loc + hook +
		" action-path=" + joinPath(e.ActionPath)
}

// HookFailure 是后置钩子聚合报告中的单条失败记录。
type HookFailure struct {
	HookName   string // 钩子名
	ObjectType string // 钩子注册的对象类型
	Message    string // 钩子报告的原因
}

// PostHookError 是后置钩子失败的聚合错误：
// 按钩子注册顺序收集全部失败结果一并报告。
type PostHookError struct {
	ActionPath []string      // 最外层动作的调用路径
	Failures   []HookFailure // 全部失败，按钩子注册顺序排列
}

// Error 实现 error 接口。
func (e *PostHookError) Error() string {
	msg := KindPostHook.String() + ": " +
		strconv.Itoa(len(e.Failures)) + " post-hook failure(s)"
	for _, f := range e.Failures {
		msg += "\n  - " + f.HookName + " on " + f.ObjectType + ": " + f.Message
	}
	msg += "\n  action-path=" + joinPath(e.ActionPath)
	return msg
}

// KindOf 返回错误的归一化类别，三类错误可相互区分。
// 非本模块产生的错误返回 -1。
func KindOf(err error) Kind {
	if err == nil {
		return -1
	}
	var single *Error
	if errors.As(err, &single) {
		return single.Kind
	}
	var agg *PostHookError
	if errors.As(err, &agg) {
		return KindPostHook
	}
	return -1
}

// IsKind 判断错误是否属于给定归一化类别。
func IsKind(err error, k Kind) bool { return KindOf(err) == k }

// AsPostHook 若 err 是后置钩子聚合错误则返回之，否则返回 nil。
func AsPostHook(err error) *PostHookError {
	var agg *PostHookError
	if errors.As(err, &agg) {
		return agg
	}
	return nil
}

func joinPath(path []string) string {
	if len(path) == 0 {
		return "(none)"
	}
	return strings.Join(path, " -> ")
}
