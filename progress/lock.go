package progress

import (
	"errors"
	"time"
)

// Clock 是可注入的时间源。
type Clock func() time.Time

var (
	// ErrBatchBusy 表示批次正被另一个活跃导入器占用。
	ErrBatchBusy = errors.New("batch is being imported")
)

// Lock 是基于进度目录的批次占用标记，崩溃后可依 TTL 安全接管。
type Lock struct {
	path  string
	token string
}

// Acquire 尝试获取占用；持有者仍活跃返回 ErrBatchBusy，过期则接管。
func Acquire(dir, batchID string, now Clock, ttl time.Duration) (*Lock, error) {
	return nil, nil
}

// Release 释放占用；仅持有者（token 匹配）可删除锁文件。
func (l *Lock) Release() error { return nil }
