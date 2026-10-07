// Package ontology 提供本体平台的核心领域能力。
//
// 本文件定义校验钩子机制的公共类型：校验对象、钩子、分组声明与注册项。
package ontology

import (
	"context"
	"errors"
)

// Decision 是单个钩子或一次校验调用的结论。
type Decision int

const (
	// Allow 表示放行。
	Allow Decision = iota
	// Reject 表示拒绝。
	Reject
)

func (d Decision) String() string {
	switch d {
	case Allow:
		return "allow"
	case Reject:
		return "reject"
	default:
		return "unknown"
	}
}

// Target 描述一次校验调用的对象：对象类型或链接类型。
type Target struct {
	// Kind 为 "objectType" 或 "linkType"。
	Kind string
	// Name 为目标标识。
	Name string
}

// Hook 是挂载在对象类型或链接类型上的校验钩子。
//
// 实现应当是无副作用可重入的：同一个 Hook 可能被并发的校验调用同时执行。
type Hook interface {
	Validate(ctx context.Context, target Target) (Decision, error)
}

// HookFunc 把普通函数适配为 Hook。
type HookFunc func(ctx context.Context, target Target) (Decision, error)

// Validate 实现 Hook 接口。
func (f HookFunc) Validate(ctx context.Context, target Target) (Decision, error) {
	return f(ctx, target)
}

// GroupSpec 声明一个优先级分组。
type GroupSpec struct {
	// Name 是分组标识，全局唯一。
	Name string
	// Priority 是组间优先级，数值越小优先级越高（越先执行）。
	// 相同 Priority 的分组按 Name 字典序排列，保证顺序确定。
	Priority int
	// ShortCircuit 决定组内是否允许短路：
	// true 时组内某个钩子拒绝后立即停止组内后续钩子；
	// false 时执行完组内全部钩子后再汇总结论。
	ShortCircuit bool
}

// HookRegistration 是一次钩子注册请求。
type HookRegistration struct {
	// Group 为已声明的分组名。
	Group string
	// ID 为钩子在分组内的标识，分组内唯一。
	ID string
	// Hook 为钩子本体。
	Hook Hook
}

var (
	// ErrGroupExists 表示重复声明同名分组。
	ErrGroupExists = errors.New("ontology: group already declared")
	// ErrGroupNotDeclared 表示向未声明的分组注册钩子。
	ErrGroupNotDeclared = errors.New("ontology: group not declared")
	// ErrHookExists 表示同一分组内重复注册相同标识的钩子。
	ErrHookExists = errors.New("ontology: hook already registered")
	// ErrHookNotFound 表示注销一个不存在的钩子。
	ErrHookNotFound = errors.New("ontology: hook not found")
	// ErrInvalidRegistration 表示注册请求本身不合法（空分组、空标识或空钩子）。
	ErrInvalidRegistration = errors.New("ontology: invalid hook registration")
)
