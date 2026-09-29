package cep

import "errors"

// 可区分的拒绝原因，调用方可用 errors.Is 判定。
var (
	// ErrInvalidWindow 窗口上限非法（<= 0）。
	ErrInvalidWindow = errors.New("cep: invalid window")
	// ErrInvalidMaxPending 待匹配队列上限非法（<= 0）。
	ErrInvalidMaxPending = errors.New("cep: invalid max pending")
	// ErrEmptyEventType 配置或事件中的类型为空。
	ErrEmptyEventType = errors.New("cep: empty event type")
	// ErrSameEventType 先事件类型与后事件类型相同，无法构成先后配对。
	ErrSameEventType = errors.New("cep: first and second type must differ")
	// ErrInvalidMode 连续性模式非法。
	ErrInvalidMode = errors.New("cep: invalid contiguity mode")
	// ErrEmptyKey 事件键为空。
	ErrEmptyKey = errors.New("cep: empty event key")
	// ErrNonMonotonicTime 同一键的时间戳出现倒退。
	ErrNonMonotonicTime = errors.New("cep: non-monotonic timestamp for key")
	// ErrPendingLimitExceeded 待匹配先事件队列超过上限。
	ErrPendingLimitExceeded = errors.New("cep: pending queue limit exceeded")
)
