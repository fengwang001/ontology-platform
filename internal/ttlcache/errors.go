package ttlcache

import "errors"

var (
	// ErrInvalidCapacity 表示 New 收到的 capacity <= 0。
	ErrInvalidCapacity = errors.New("ttlcache: capacity must be positive")
	// ErrNilClock 表示 New 收到的 now 时钟函数为 nil。
	ErrNilClock = errors.New("ttlcache: now clock func must not be nil")
	// ErrInvalidTTL 表示 Put 收到的 ttlMillis <= 0。
	ErrInvalidTTL = errors.New("ttlcache: ttlMillis must be positive")
)
