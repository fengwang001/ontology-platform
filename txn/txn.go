// Package txn 维护事务号与提交状态，状态只存进程内存。
package txn

import (
	"errors"
	"sync"
)

// ErrUnknown 是三类可判定错误之一：查询了未登记的事务号。
var ErrUnknown = errors.New("txn: unknown transaction id")

// ID 是事务号。
type ID uint64

// Status 为事务当前状态。
type Status uint8

const (
	// Active 表示事务仍在进行。
	Active Status = iota
	// Committed 表示事务已提交。
	Committed
	// Aborted 表示事务已回滚。
	Aborted
)

type entry struct {
	status Status
	commit uint64 // 仅在 status == Committed 时有意义
}

// Registry 是进程内事务表。
type Registry struct {
	mu   sync.RWMutex
	next ID
	txns map[ID]entry
}

// NewRegistry 创建空事务表。
func NewRegistry() *Registry {
	return &Registry{txns: make(map[ID]entry)}
}

// Begin 登记一个 Active 事务并返回其事务号。
func (r *Registry) Begin() ID {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := r.next
	r.next++
	r.txns[id] = entry{status: Active}
	return id
}

// Commit 以给定提交号提交事务。
func (r *Registry) Commit(t ID, commitNum uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.txns[t]
	if !ok {
		return ErrUnknown
	}
	e.status, e.commit = Committed, commitNum
	r.txns[t] = e
	return nil
}

// Abort 回滚事务。
func (r *Registry) Abort(t ID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.txns[t]
	if !ok {
		return ErrUnknown
	}
	e.status = Aborted
	r.txns[t] = e
	return nil
}

// Lookup 返回事务状态与提交号，计数为一次表查找。
func (r *Registry) Lookup(t ID) (Status, uint64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.txns[t]
	if !ok {
		return Active, 0, ErrUnknown
	}
	return e.status, e.commit, nil
}

// All 返回事务号快照，仅供朴素参考实现遍历事务表。
func (r *Registry) All() []ID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]ID, 0, len(r.txns))
	for id := range r.txns {
		ids = append(ids, id)
	}
	return ids
}
