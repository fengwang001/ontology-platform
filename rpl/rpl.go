// Package rpl 按 gph 的可回放集执行回放，并维护已回放集合。
package rpl

import "ontology/gph"

// Replayer 在 gph.Graph 上执行可复现回放。非并发安全，由上层加锁。
type Replayer struct {
	g        *gph.Graph
	replayed map[int]bool
	checked  int // 最近一次 Replay 检查过的事务个数
}

// New 在图 g 上创建回放器。
func New(g *gph.Graph) *Replayer {
	return &Replayer{g: g, replayed: map[int]bool{}}
}

// Commit 登记事务，委托给底层图。
func (r *Replayer) Commit(id int, deps []int) error { return r.g.Commit(id, deps) }

// Replay 反复选取可回放事务中 id 最小者，直到没有可回放事务。
func (r *Replayer) Replay() []int { return nil }

// Replayed 返回已回放集合的副本。
func (r *Replayer) Replayed() map[int]bool { return nil }

// Staged 返回暂存事务 id，升序。
func (r *Replayer) Staged() []int { return r.g.Staged() }
