package upvalue

import "errors"

// 可区分的拒绝原因。被拒绝的操作不改变栈、栈顶与任何捕获变量。
var (
	// ErrSlotOutOfRange 捕获的槽号不小于栈顶。
	ErrSlotOutOfRange = errors.New("upvalue: capture slot out of range")
	// ErrInvalidLevel 关闭层小于 0 或大于栈顶。
	ErrInvalidLevel = errors.New("upvalue: close level out of range")
	// ErrStackOutOfRange 读写栈槽越界。
	ErrStackOutOfRange = errors.New("upvalue: stack slot out of range")
	// ErrHandleNotFound 句柄不存在（与 ErrHandleReleased 互斥）。
	ErrHandleNotFound = errors.New("upvalue: handle not found")
	// ErrHandleReleased 句柄持有数已减到 0（与 ErrHandleNotFound 互斥）。
	ErrHandleReleased = errors.New("upvalue: handle fully released")
)
