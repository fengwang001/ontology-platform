package ttlcache

import "errors"

var (
	// ErrInvalidCapacity 表示 New 收到的 capacity <= 0。
	ErrInvalidCapacity = errors.New("ttlcache: capacity must be positive")
	// ErrInvalidTTL 表示 Put 收到的 ttlMillis <= 0。
	ErrInvalidTTL = errors.New("ttlcache: ttl must be positive")
)
