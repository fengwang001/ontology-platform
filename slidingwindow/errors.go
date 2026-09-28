package slidingwindow

import (
	"errors"
)

// 可区分的拒绝原因。调用方可使用 errors.Is 精确判定。
var (
	// ErrInvalidCapacity 在容量小于 1 时返回。
	ErrInvalidCapacity = errors.New("slidingwindow: capacity must be a positive integer")
	// ErrEmptyEvict 在对空窗口执行显式逐出时返回。
	ErrEmptyEvict = errors.New("slidingwindow: cannot evict from an empty window")
	// ErrEmptyMax 在对空窗口求最大值时返回。
	ErrEmptyMax = errors.New("slidingwindow: cannot get max of an empty window")
	// ErrInvalidValue 在写入 NaN、+Inf 或 -Inf 时返回。
	ErrInvalidValue = errors.New("slidingwindow: value must be a finite number")
	// ErrEmptyBatch 在批量写入空切片时返回。
	ErrEmptyBatch = errors.New("slidingwindow: batch must contain at least one value")
)
