// Package keygroup 提供键组划分与扩缩容状态重分配能力。
//
// 键先经哈希映射到固定数量（maxParallelism）的键组，
// 键组再按连续区间分配给若干实例；并行度改变时，
// 仅迁移归属发生变化的键组，归属不变的键组原地不动。
package keygroup

import "errors"

// 可区分的非法输入原因，使用 errors.Is 判定。
var (
	// ErrInvalidMaxParallelism 键组总数非法（<= 0）。
	ErrInvalidMaxParallelism = errors.New("keygroup: maxParallelism 必须为正整数")
	// ErrInvalidParallelism 并行度非法（<= 0 或 > maxParallelism）。
	ErrInvalidParallelism = errors.New("keygroup: parallelism 必须满足 1 <= parallelism <= maxParallelism")
	// ErrInvalidOperatorIndex 实例下标越界（< 0 或 >= parallelism）。
	ErrInvalidOperatorIndex = errors.New("keygroup: 实例下标必须满足 0 <= index < parallelism")
	// ErrEmptyKey 键为空。
	ErrEmptyKey = errors.New("keygroup: 键不能为空")
)

// Error 携带可区分的原因与上下文，Is 委托给底层 sentinel。
type Error struct {
	Op     string // 触发错误操作名
	Reason error  // 上述 sentinel 之一
	Detail string // 具体参数说明
}

func (e *Error) Error() string {
	return e.Op + ": " + e.Reason.Error() + " (" + e.Detail + ")"
}

func (e *Error) Unwrap() error { return e.Reason }

func (e *Error) Is(target error) bool { return errors.Is(e.Reason, target) }

func invalidInput(op string, reason error, detail string) *Error {
	return &Error{Op: op, Reason: reason, Detail: detail}
}
