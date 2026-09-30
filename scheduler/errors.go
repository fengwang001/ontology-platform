package scheduler

import "errors"

// 可区分的拒绝原因。所有错误都在操作产生任何副作用之前返回。
var (
	// ErrClockRewind：时刻早于此前见过的任一时刻。
	ErrClockRewind = errors.New("scheduler: clock rewind: now is earlier than a previously seen time")
	// ErrNegativeSector：扇区为负。
	ErrNegativeSector = errors.New("scheduler: negative sector")
	// ErrDuplicateID：标识与某个已提交、已派发或已取消的请求重复。
	ErrDuplicateID = errors.New("scheduler: duplicate request id")
	// ErrNotFound：取消的标识从未提交或已被取消。
	ErrNotFound = errors.New("scheduler: request not found")
	// ErrAlreadyDispatched：取消的请求已派发（每个请求只能被取消或派发一次）。
	ErrAlreadyDispatched = errors.New("scheduler: request already dispatched")
	// ErrEmptyQueue：派发时两个队列都为空。
	ErrEmptyQueue = errors.New("scheduler: no pending request")

	// 配置错误。
	ErrInvalidReadDeadline  = errors.New("scheduler: read deadline Er must be positive")
	ErrInvalidWriteDeadline = errors.New("scheduler: write deadline Ew must be positive")
	ErrInvalidStarveX       = errors.New("scheduler: write starve limit X must be >= 1")
)
