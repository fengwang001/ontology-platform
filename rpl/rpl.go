// Package rpl 按 gph 的可回放集执行回放：反复选取可回放事务中 id 最小者，
// 维护已回放集合。Replay 只走就绪堆，不整表扫描。本包不做并发控制。
package rpl

import (
	"container/heap"

	"ontology/gph"
)

// intHeap 是最小堆，保证 id 升序 tie-break，回放顺序可复现。
type intHeap []int

func (h intHeap) Len() int           { return len(h) }
func (h intHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h intHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *intHeap) Push(x any)        { *h = append(*h, x.(int)) }
func (h *intHeap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}

type Replayer struct {
	g        *gph.Graph
	replayed map[int]bool
	ready    intHeap
	checked  int // 非导出：最近一次 Replay 检查过的事务个数，仅供包内测试观测
}

func New(g *gph.Graph) *Replayer {
	return &Replayer{g: g, replayed: map[int]bool{}}
}

// Commit 登记事务；新变为可回放的事务进入就绪堆。
func (r *Replayer) Commit(id int, deps []int) error {
	ready, err := r.g.Commit(id, deps)
	if err != nil {
		return err
	}
	for _, x := range ready {
		heap.Push(&r.ready, x)
	}
	return nil
}

// Replay 反复弹出堆中最小 id 并标记已回放，直到没有可回放事务。
func (r *Replayer) Replay() []int {
	r.checked = 0
	out := []int{}
	for r.ready.Len() > 0 {
		id := heap.Pop(&r.ready).(int)
		if r.replayed[id] {
			continue
		}
		r.checked++
		out = append(out, id)
		r.replayed[id] = true
		for _, n := range r.g.MarkReplayed(id) {
			heap.Push(&r.ready, n)
		}
	}
	return out
}

// Replayed 返回已回放集合的副本。
func (r *Replayer) Replayed() map[int]bool {
	out := make(map[int]bool, len(r.replayed))
	for k, v := range r.replayed {
		out[k] = v
	}
	return out
}

// Staged 返回当前暂存事务（升序）。
func (r *Replayer) Staged() []int { return r.g.Staged() }
