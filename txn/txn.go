// Package txn 维护事务号与提交状态（进程内存，仅标准库）。
package txn

import (
	"errors"
	"sync"
	"sync/atomic"
)

// ErrUnknownTxn 表示引用了一个从未注册的事务号。
var ErrUnknownTxn = errors.New("txn: unknown transaction id")

// Status 是事务的生命周期状态。
type Status uint8

const (
	Active Status = iota
	Committed
	Aborted
)

// Info 是一次事务表查找返回的全部信息（只做一次表查找即可完成判定）。
type Info struct {
	ID       int64
	Status   Status
	CommitTS int64 // 仅 Committed 时有意义
}

// Registry 是进程内存中的事务表。
type Registry struct {
	mu      sync.RWMutex
	nextID  atomic.Int64
	entries map[int64]Info
}

// NewRegistry 创建空事务表，事务号从 1 开始分配。
func NewRegistry() *Registry {
	return &Registry{entries: make(map[int64]Info)}
}

// Begin 注册一个新事务并返回事务号。
func (r *Registry) Begin() int64 {
	id := r.nextID.Add(1)
	r.mu.Lock()
	r.entries[id] = Info{ID: id, Status: Active}
	r.mu.Unlock()
	return id
}

// Commit 把事务标记为已提交并记录提交号。
func (r *Registry) Commit(id, commitTS int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, ok := r.entries[id]
	if !ok {
		return ErrUnknownTxn
	}
	info.Status, info.CommitTS = Committed, commitTS
	r.entries[id] = info
	return nil
}

// Abort 把事务标记为已回滚，回滚事务的版本永不可见。
func (r *Registry) Abort(id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, ok := r.entries[id]
	if !ok {
		return ErrUnknownTxn
	}
	info.Status = Aborted
	r.entries[id] = info
	return nil
}

// Lookup 执行一次事务表查找，返回事务信息；不存在时 ok 为 false。
func (r *Registry) Lookup(id int64) (Info, bool) {
	r.mu.RLock()
	info, ok := r.entries[id]
	r.mu.RUnlock()
	return info, ok
}

// Entries 返回事务表快照副本，仅供朴素参考实现/测试遍历使用。
func (r *Registry) Entries() []Info {
	r.mu.RLock()
	all := make([]Info, 0, len(r.entries))
	for _, info := range r.entries {
		all = append(all, info)
	}
	r.mu.RUnlock()
	return all
}
