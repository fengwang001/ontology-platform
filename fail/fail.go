// Package fail 定义任务的四类终态与可 errors.Is 判定的错误类型。
package fail

import (
	"errors"
	"fmt"
)

// State 是任务的最终状态枚举，四类终态互斥且可区分。
type State int

const (
	// Pending 尚未调度。
	Pending State = iota
	// Running 正在执行（非终态）。
	Running
	// Succeeded 已成功完成。
	Succeeded
	// Failed 真实执行并返回错误（含 panic）。
	Failed
	// Skipped 从未开始，因上游失败被跳过。
	Skipped
	// Canceled 已开始执行，因旁支失败被取消。
	Canceled
)

func (s State) String() string {
	switch s {
	case Pending:
		return "pending"
	case Running:
		return "running"
	case Succeeded:
		return "succeeded"
	case Failed:
		return "failed"
	case Skipped:
		return "skipped"
	case Canceled:
		return "canceled"
	}
	return "unknown"
}

// ErrPanic 用于判定错误是否由任务 panic 转换而来。
var ErrPanic = errors.New("task panicked")

// PanicError 携带 panic 的原始信息，Unwrap 返回 ErrPanic。
type PanicError struct {
	Task  string
	Value any
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("task %s panicked: %v", e.Task, e.Value)
}

// Unwrap 使 errors.Is(err, ErrPanic) 成立。
func (e *PanicError) Unwrap() error { return ErrPanic }
