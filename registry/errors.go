// Package registry 为每个业务 ID 保存至多一条最近实例记录，并按复用/冲突策略仲裁启动。
package registry

import "errors"

var (
	ErrRunning    = errors.New("registry: existing instance is running")
	ErrDenied     = errors.New("registry: terminate denied")
	ErrReuse      = errors.New("registry: reuse rejected by policy")
	ErrCapacity   = errors.New("registry: capacity exceeded")
	ErrClock      = errors.New("registry: now is before current clock")
	ErrNotFound   = errors.New("registry: live record not found")
	ErrStale      = errors.New("registry: stale run number")
	ErrNotRunning = errors.New("registry: record is not running")
	ErrArgument   = errors.New("registry: invalid argument")
)
