package hooks

import (
	"context"
)

// Phase 是生命周期阶段标识。定义在本包而非状态机包中，
// 使钩子模块不依赖状态机模块，避免循环依赖。
type Phase string

// CommitSemantics 声明钩子副作用（如校验日志）在转移被拒绝时的去留。
type CommitSemantics int

const (
	// RollbackOnFailure 转移被拒绝时，该钩子暂存的副作用一并撤销。
	RollbackOnFailure CommitSemantics = iota
	// AutoCommit 即使转移被拒绝，该钩子已记录的副作用仍然保留。
	AutoCommit
)

// SideEffect 是钩子在校验过程中记录的一条副作用（如一条校验日志）。
type SideEffect struct {
	Hook string
	Note string
}

// Context 是单次钩子调用的上下文。钩子通过 Record 暂存副作用，
// 由状态机模块按钩子声明的 CommitSemantics 决定提交或撤销。
type Context struct {
	InstanceID string
	From       Phase
	To         Phase

	hookName string
	effects  []SideEffect
}

// NewContext 构造一次钩子调用的上下文。
func NewContext(instanceID string, from, to Phase, hookName string) *Context {
	return &Context{
		InstanceID: instanceID,
		From:       from,
		To:         to,
		hookName:   hookName,
	}
}

// Record 暂存一条副作用记录。
func (c *Context) Record(note string) {
	c.effects = append(c.effects, SideEffect{Hook: c.hookName, Note: note})
}

// Effects 返回本次调用暂存的全部副作用。
func (c *Context) Effects() []SideEffect { return c.effects }

// Hook 是生命周期校验钩子。
type Hook interface {
	// Name 返回钩子的稳定名称，用于触发记录与错误信息。
	Name() string
	// Semantics 声明副作用的提交语义。
	Semantics() CommitSemantics
	// Validate 执行校验；返回非 nil 错误则整个转移被拒绝。
	Validate(ctx context.Context, tc *Context) error
}

// Func 是 Hook 的函数式便捷实现。
type Func struct {
	HookName string
	Commit   CommitSemantics
	Fn       func(ctx context.Context, tc *Context) error
}

// Name 实现 Hook。
func (f Func) Name() string { return f.HookName }

// Semantics 实现 Hook。
func (f Func) Semantics() CommitSemantics { return f.Commit }

// Validate 实现 Hook。
func (f Func) Validate(ctx context.Context, tc *Context) error {
	return f.Fn(ctx, tc)
}
