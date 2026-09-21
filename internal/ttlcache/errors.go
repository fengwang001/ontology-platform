package ttlcache

import "errors"

var (
	// ErrInvalidCapacity 在 New 收到非正容量时返回。
	ErrInvalidCapacity = errors.New("ttlcache: capacity must be positive")
	// ErrInvalidTTL 在 Put 收到非正 TTL 时返回。
	ErrInvalidTTL = errors.New("ttlcache: ttlMillis must be positive")
	// ErrNilClock 在 New 收到 nil 时钟函数时返回。
	ErrNilClock = errors.New("ttlcache: now clock function must not be nil")
)
