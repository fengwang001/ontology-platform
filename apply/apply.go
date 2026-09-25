// Package apply 组合「查重 → 应用」，在持锁临界区内原子完成。
package apply

import (
	"sync"

	"ontology/dedup"
)

// Applier 持有计数表与已应用集。输入合法性由上层（api）校验，
// 这里只保证原子性与幂等语义。
type Applier struct {
	mu    sync.Mutex
	state map[string]int
	set   *dedup.Set
}

// New 返回空 Applier。
func New() *Applier {
	return &Applier{state: make(map[string]int), set: dedup.New()}
}

// Apply 若 txid 已应用则幂等跳过（不改任何状态），返回 false；
// 否则 state[key] += delta 并把 txid 入集，返回 true。
// 查重与应用在同一临界区，并发同一 txid 也至多应用一次。
func (a *Applier) Apply(txid int64, key string, delta int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.set.Seen(txid) {
		return false
	}
	a.state[key] += delta
	a.set.Add(txid)
	return true
}

// Restore 用给定的已应用 txid 列表重建已应用集（不触碰 state）。
// 列表合法性由上层校验。
func (a *Applier) Restore(applied []int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.set = dedup.New()
	for _, txid := range applied {
		a.set.Add(txid)
	}
}

// Snapshot 返回状态副本与已应用 txid 升序列表。
func (a *Applier) Snapshot() (map[string]int, []int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := make(map[string]int, len(a.state))
	for k, v := range a.state {
		st[k] = v
	}
	return st, a.set.Snapshot()
}
