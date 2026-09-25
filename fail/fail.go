// Package fail 定义任务状态枚举、运行模式与错误哨兵，供各包统一判定。
package fail

import "errors"

// Status 是任务状态，四类终态互相可区分。
type Status int

const (
	Pending  Status = iota // 已入图，尚未就绪
	Ready                  // 依赖已全部成功，等待派发
	Running                // 已派发，函数已开始执行
	Success                // 函数已返回 nil
	Failed                 // 函数返回 error 或 panic
	Skipped                // 从未开始，且存在失败祖先
	Canceled               // 被失败事件中止（可能已开始，可能未开始）
)

// String 返回状态的可读名，供报告逐字节输出。
func (s Status) String() string {
	switch s {
	case Pending:
		return "pending"
	case Ready:
		return "ready"
	case Running:
		return "running"
	case Success:
		return "success"
	case Failed:
		return "failed"
	case Skipped:
		return "skipped"
	case Canceled:
		return "canceled"
	}
	return "unknown"
}

// Mode 是失败传播模式。
type Mode int

const (
	FastFail   Mode = iota // 默认：首个失败即传播并取消旁支
	BestEffort             // 尽力而为：不依赖失败任务的分支继续跑完
)

// 错误哨兵，调用方用 errors.Is 区分。
var (
	ErrCycle      = errors.New("cycle detected")
	ErrMissingDep = errors.New("dependency on unknown task")
	ErrPanic      = errors.New("task panicked")
	ErrCanceled   = errors.New("task canceled")
	ErrSkipped    = errors.New("task skipped")
)

// PanicError 包装任务 panic 的原始值；errors.Is(err, ErrPanic) 为真。
type PanicError struct {
	Value any
}

func (e *PanicError) Error() string { return "task panicked: " + stringify(e.Value) }
func (e *PanicError) Unwrap() error { return ErrPanic }

func stringify(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if err, ok := v.(error); ok {
		return err.Error()
	}
	return "non-string panic"
}
