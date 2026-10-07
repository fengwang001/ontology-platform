package validate

import (
	"context"
	"fmt"
)

// Priority 是分组在分组之间的执行优先级声明。
// 数值越大优先级越高，越先执行；同值时按分组名的字典序作为确定的兜底次序，
// 避免依赖 Go map 的随机遍历顺序。
type Priority int

// GroupSpec 声明一个优先级分组。
//
// ShortCircuit 决定该分组内部的短路语义：
//   - true：组内按注册顺序执行，某个钩子一旦拒绝，立即停止执行组内后续钩子。
//   - false：必须执行完组内全部钩子，再汇总结论（任一拒绝即该组拒绝）。
//
// 组与组之间总是短路的：高优先级分组拒绝（或钩子异常）后，低优先级分组
// 一律不得执行。
type GroupSpec struct {
	Name         string
	Priority     Priority
	ShortCircuit bool
}

// Decision 是单个钩子对一次校验的结论。
type Decision int

const (
	// Approve 表示钩子通过。
	Approve Decision = iota
	// Reject 表示钩子明确拒绝。
	Reject
)

// Outcome 是单个钩子的执行结果。
type Outcome struct {
	Decision Decision
	// Reason 可用于审计与最终判定依据，不参与调度。
	Reason string
}

// HookFunc 是钩子函数形态。返回错误表示钩子自身异常，该异常会被上抛为
// HookExecutionError，分组结果不可判定，且更低优先级分组不得执行；
// 它绝不被归并为普通 Reject。
type HookFunc[T any] func(ctx context.Context, target T) (Outcome, error)

// Hook 是一个具体的已注册钩子。
type Hook[T any] struct {
	// ID 是钩子在同一 Registry 内的唯一标识；重复注册返回错误。
	ID string
	// Group 必须是已通过 DeclareGroup 声明的分组名。
	Group string
	Fn    HookFunc[T]
}

func (h Hook[T]) validate() error {
	if h.ID == "" {
		return fmt.Errorf("validate: hook id must not be empty")
	}
	if h.Group == "" {
		return fmt.Errorf("validate: hook %q group must not be empty", h.ID)
	}
	if h.Fn == nil {
		return fmt.Errorf("validate: hook %q fn must not be nil", h.ID)
	}
	return nil
}
