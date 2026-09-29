package replica

import "errors"

// 可区分的拒绝原因。任一被拒操作都不会改变同步副本集、
// 高水位或任何副本进度（失败不留痕）。
var (
	// ErrInvalidArgument 参数本身非法（空副本名、重复注册、空时间等）。
	ErrInvalidArgument = errors.New("replica: invalid argument")
	// ErrUnknownReplica 操作引用了从未注册的副本。
	ErrUnknownReplica = errors.New("replica: unknown replica")
	// ErrInvalidOffset 位点非法（越界或回退）。
	ErrInvalidOffset = errors.New("replica: invalid offset")
	// ErrClockRollback 提供的时间早于系统已观察到的最新时间。
	ErrClockRollback = errors.New("replica: clock rolled back")
)
