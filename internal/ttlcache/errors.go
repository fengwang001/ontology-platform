package ttlcache

import "errors"

var (
	// ErrInvalidCapacity 表示 New 收到了非正数的容量。
	ErrInvalidCapacity = errors.New("ttlcache: capacity must be positive")
	// ErrInvalidTTL 表示 Put 收到了非正数的 TTL。
	ErrInvalidTTL = errors.New("ttlcache: ttlMillis must be positive")
)
