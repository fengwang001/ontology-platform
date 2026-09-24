// Package txn 保存进程内存中的事务号与提交状态。
package txn

import (
	"errors"
	"sync"
)

// ErrUnknownTxn 表示判定时引用了事务表里不存在的事务号。
var ErrUnknownTxn = errors.New("txn: unknown transaction id")

// State 是事务的生命周期状态。
type State uint8

const (
	Active    State = iota // 进行中
	Committed              // 已提交
	Aborted                // 已回滚
)

// ID 是单调分配的事务号，从 1 开始。
type ID uint64

type entry struct {
	state    State
	commitNo uint64
}

// Registry 是并发安全的内存事务表。
type Registry struct {
	mu      sync.RWMutex
	entries []entry // 下标为 ID-1
}

// Begin 注册一个新事务并返回其事务号。
func (r *Registry) Begin() ID {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, entry{state: Active})
	return ID(len(r.entries))
}

// Commit 以给定提交号标记事务已提交。
func (r *Registry) Commit(id ID, commitNo uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id == 0 || int(id) > len(r.entries) {
		return ErrUnknownTxn
	}
	r.entries[id-1] = entry{state: Committed, commitNo: commitNo}
	return nil
}

// Abort 标记事务已回滚，回滚事务对任何快照永不可见。
func (r *Registry) Abort(id ID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id == 0 || int(id) > len(r.entries) {
		return ErrUnknownTxn
	}
	r.entries[id-1].state = Aborted
	return nil
}

// Info 一次返回事务状态与提交号，记为一次事务表查找。
func (r *Registry) Info(id ID) (State, uint64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if id == 0 || int(id) > len(r.entries) {
		return Active, 0, ErrUnknownTxn
	}
	e := r.entries[id-1]
	return e.state, e.commitNo, nil
}

// Len 返回已注册事务总数，供测试朴素实现遍历。
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}
