package history

import "errors"

// 参数与容量相关的哨兵错误。replay 等上层包用 errors.Is 区分拒绝类别。
var (
	// ErrArgument 表示参数非法：工作流名为空、事件中的 name/pid 为空、
	// 事件种类未知等。
	ErrArgument = errors.New("history: invalid argument")

	// ErrConflict 表示 Append 的 expect 与日志当前长度不一致：
	// 已有并发写入抢先发生。
	ErrConflict = errors.New("history: append conflict")

	// ErrCapacity 表示追加后该工作流事件总数超过 MaxEventsPerWorkflow。
	ErrCapacity = errors.New("history: workflow event capacity exceeded")
)
