// Package fail 定义任务终态枚举与可判定的哨兵错误。
package fail

import (
	"errors"
	"fmt"
)

// 哨兵错误，配合 errors.Is 使用。
var (
	ErrCycle    = errors.New("graph contains a cycle")
	ErrUnknown  = errors.New("unknown task")
	ErrCanceled = errors.New("task canceled")
	ErrSkipped  = errors.New("task skipped")
	ErrPanic    = errors.New("task panicked")
)

// State 是任务的终态（或中间态）枚举。
type State int

const (
	Pending State = iota
	Running
	Succeeded
	Failed
	Skipped
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

// PanicError 包装任务函数 panic 时 recover 到的值。
type PanicError struct {
	Value any
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("%v: %v", ErrPanic, e.Value)
}

// Is 使 errors.Is(err, ErrPanic) 成立。
func (e *PanicError) Is(target error) bool {
	return target == ErrPanic
}
