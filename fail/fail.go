// Package fail 定义任务的终态枚举、错误哨兵与传播模式。
package fail

import (
	"errors"
	"fmt"
)

// Status 是任务的最终状态，四类互相可区分。
type Status int

const (
	// Success 已成功：失败传播前已完成，状态不可回滚。
	Success Status = iota
	// Failed 失败：真正执行并返回错误（含 panic 转换）。
	Failed
	// Skipped 被跳过：从未开始执行，原因指向最初失败任务。
	Skipped
	// Canceled 被取消：失败发生时已在执行，被上下文取消。
	Canceled
)

// String 返回状态的规范名称，用于确定性报告。
func (s Status) String() string {
	switch s {
	case Success:
		return "SUCCESS"
	case Failed:
		return "FAILED"
	case Skipped:
		return "SKIPPED"
	case Canceled:
		return "CANCELED"
	default:
		return "UNKNOWN"
	}
}

// Mode 是失败传播模式。
type Mode int

const (
	// FailFast 快速失败：首个失败即取消全部在途、跳过其余未开始任务。
	FailFast Mode = iota
	// BestEffort 尽力而为：不依赖失败任务的分支继续跑完。
	BestEffort
)

var (
	// ErrCycle 表示图中存在环。
	ErrCycle = errors.New("fail: cycle detected")
	// ErrPanic 表示任务发生 panic，已被恢复并转为失败。
	ErrPanic = errors.New("fail: task panicked")
)

// PanicError 包装任务 panic 的原始信息，支持 errors.Is(err, ErrPanic)。
type PanicError struct {
	TaskID string
	Value  any
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("%s: task %q panic: %v", ErrPanic, e.TaskID, e.Value)
}
func (e *PanicError) Unwrap() error { return ErrPanic }

// RootError 表示某任务因最初失败任务 root 而被跳过/取消。
// 用 errors.Is(err, RootError(id)) 可定位最初失败任务。
type RootError struct {
	Root string
}

func (e *RootError) Error() string {
	return fmt.Sprintf("fail: blocked by failed task %q", e.Root)
}

// Is 使 errors.Is 能按 root 任务 ID 匹配。
func (e *RootError) Is(target error) bool {
	t, ok := target.(*RootError)
	return ok && t.Root == e.Root
}

// RootCause 返回用于 errors.Is 的哨兵：跳过/取消原因是否指向 root。
func RootCause(root string) error {
	return &RootError{Root: root}
}
