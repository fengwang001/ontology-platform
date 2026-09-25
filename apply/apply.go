// Package apply 组合「查重 → 应用」，维护 Key → 累计值 计数表。
package apply

import (
	"ontology/dedup"
	"sync"
)

// 哨兵错误：三类非法输入与非法恢复列表，互不相同。
var (
	ErrInvalidTxID    = errInvalidTxID{}
	ErrEmptyKey       = errEmptyKey{}
	ErrZeroDelta      = errZeroDelta{}
	ErrInvalidRestore = errInvalidRestore{}
)

type errInvalidTxID struct{}
type errEmptyKey struct{}
type errZeroDelta struct{}
type errInvalidRestore struct{}

func (errInvalidTxID) Error() string    { return "apply: txid must be positive" }
func (errEmptyKey) Error() string       { return "apply: key must not be empty" }
func (errZeroDelta) Error() string      { return "apply: delta must not be zero" }
func (errInvalidRestore) Error() string { return "apply: restore list contains non-positive txid" }

// Engine 是去重应用器；一把互斥锁保护 state 与 dedup 集合，
// 保证「查重 → 改 state → 入集」对每个 txid 原子且至多一次。
type Engine struct {
	mu    sync.Mutex
	set   *dedup.Set
	state map[string]int
}

// New 创建空 Engine。
func New() *Engine {
	return &Engine{set: dedup.New(), state: map[string]int{}}
}

// Apply 查重并（首次时）应用；重复到达幂等成功返回 applied=false。
// 顺序：txid 非法先拒（非正 txid 不可能在已应用集里）；命中已应用集则
// 一律幂等成功、不关心 key/delta 内容；只有全新 txid 才校验 key/delta。
func (e *Engine) Apply(txid int64, key string, delta int) (applied bool, err error) {
	if txid <= 0 {
		return false, ErrInvalidTxID
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.set.Seen(txid) {
		return false, nil
	}
	switch {
	case key == "":
		return false, ErrEmptyKey
	case delta == 0:
		return false, ErrZeroDelta
	}
	e.state[key] += delta
	e.set.Add(txid)
	return true, nil
}

// Snapshot 返回 state 副本与已应用 txid 升序列表。
func (e *Engine) Snapshot() (map[string]int, []int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := make(map[string]int, len(e.state))
	for k, v := range e.state {
		st[k] = v
	}
	return st, e.set.Snapshot()
}

// Restore 用给定列表整体重建已应用集；先全量校验，
// 任一 txid 非法则原样保留旧状态与旧集合（整体失败）。
func (e *Engine) Restore(applied []int64) error {
	next := make(map[int64]struct{}, len(applied))
	for _, t := range applied {
		if t <= 0 {
			return ErrInvalidRestore
		}
		next[t] = struct{}{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.set = dedup.New()
	for t := range next {
		e.set.Add(t)
	}
	return nil
}
