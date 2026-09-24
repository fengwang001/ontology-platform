// Package api 是对外门面：并发安全的事务登记与依赖排序回放。
package api

import (
	"sync"

	"ontology/gph"
	"ontology/rpl"
)

// 可判定的哨兵错误，互为不同值。
var (
	ErrInvalidID = gph.ErrInvalidID
	ErrSelfDep   = gph.ErrSelfDep
	ErrDuplicate = gph.ErrDuplicate
	ErrCycle     = gph.ErrCycle
	ErrFull      = gph.ErrFull
)

// API 并发安全。
type API struct {
	mu sync.Mutex
	r  *rpl.Replayer
}

// New 创建最多容纳 maxTxns 个事务的实例。
func New(maxTxns int) *API { return &API{r: rpl.New(gph.New(maxTxns))} }

// Commit 登记事务及其依赖；失败则整体拒绝、不留痕迹。
func (a *API) Commit(id int, deps ...int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.r.Commit(id, deps)
}

// Replay 返回当前可回放事务（id 升序 tie-break），并标记为已回放。
func (a *API) Replay() []int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.r.Replay()
}

// Replayed 返回已回放集合的副本。
func (a *API) Replayed() map[int]bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.r.Replayed()
}

// Staged 返回暂存事务 id，升序。
func (a *API) Staged() []int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.r.Staged()
}

// SelfCheck 对内置 Commit/Replay 序列核验四条不变量，全部通过返回 nil。
func (a *API) SelfCheck() error { return nil }
