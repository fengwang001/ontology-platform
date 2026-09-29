package asyncio

import "errors"

// 各类拒绝原因互不相同、可通过 errors.Is 区分。
var (
	// ErrInvalidCapacity 构造时容量非正。
	ErrInvalidCapacity = errors.New("asyncio: capacity must be positive")
	// ErrInvalidMode 构造时模式取值非法。
	ErrInvalidMode = errors.New("asyncio: invalid mode")
	// ErrEmptyID 输入/完成声明使用了空标识。
	ErrEmptyID = errors.New("asyncio: element id must not be empty")
	// ErrDuplicateID 输入的标识与某个仍在队列中的元素重复。
	ErrDuplicateID = errors.New("asyncio: duplicate element id")
	// ErrCapacityFull 容量已被在途元素占满。
	ErrCapacityFull = errors.New("asyncio: buffer at capacity")
	// ErrUnknownID 完成声明引用了队列中不存在的标识。
	ErrUnknownID = errors.New("asyncio: unknown element id")
	// ErrAlreadyCompleted 完成声明重复引用了已经声明完成的元素。
	ErrAlreadyCompleted = errors.New("asyncio: element already completed")
	// ErrWatermarkNotIncreasing 水位线未相对上一条严格递增。
	ErrWatermarkNotIncreasing = errors.New("asyncio: watermark must be strictly increasing")
)
