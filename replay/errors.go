package replay

import "errors"

// 可区分的失败原因。调用方可用 errors.Is 判别。
var (
	// ErrInvalidConfig 表示速率、桶容量或队列上限等参数不合法。
	ErrInvalidConfig = errors.New("replay: invalid config")
	// ErrClockBackwards 表示传入的逻辑时间早于当前时钟。
	ErrClockBackwards = errors.New("replay: logical clock moved backwards")
	// ErrQueueFull 表示队列已达上限，整批入队被整体拒绝。
	ErrQueueFull = errors.New("replay: queue limit exceeded")
)
