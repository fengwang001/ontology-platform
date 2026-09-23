// Package reclaim 管理回收水位与候选版本。增量策略：
// 候选在「被更新版本遮蔽」时进入以遮蔽者提交号为键的最小堆，
// 单次回收只考察堆顶，绝不全表扫描。
package reclaim

import (
	"container/heap"

	"ontology/snapshot"
	"ontology/txid"
)

// Candidate 是一个被遮蔽的旧版本。
// Commit 是被遮蔽版本的提交号，Superseder 是遮蔽它的版本的提交号。
// 仅当 Superseder < 水位时该版本才可安全回收。
type Candidate struct {
	Key        string
	Commit     txid.T
	Superseder txid.T
}

// candHeap 是以 Superseder 为键的最小堆。
type candHeap []Candidate

func (h candHeap) Len() int           { return len(h) }
func (h candHeap) Less(i, j int) bool { return h[i].Superseder < h[j].Superseder }
func (h candHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *candHeap) Push(x any)        { *h = append(*h, x.(Candidate)) }
func (h *candHeap) Pop() any {
	old := *h
	n := len(old)
	c := old[n-1]
	*h = old[:n-1]
	return c
}

// Reclaimer 推进水位并增量地产出可回收候选。
// 非并发安全：由 store 的互斥锁保护。
type Reclaimer struct {
	h        candHeap
	water    txid.T
	examined int // 非导出计数器：历次回收实际考察的版本数
}

// New 返回空回收器。
func New() *Reclaimer {
	return &Reclaimer{}
}

// Add 登记一个被遮蔽的候选版本。
func (r *Reclaimer) Add(c Candidate) {
	heap.Push(&r.h, c)
}

// Advance 用活跃快照注册表计算新水位并推进：只升不降。
// now 取事务号源的 Peek()。返回推进后的水位。
func (r *Reclaimer) Advance(reg *snapshot.Registry, now txid.T) txid.T {
	if w := reg.Horizon(now); w > r.water {
		r.water = w
	}
	return r.water
}

// Watermark 返回当前水位。
func (r *Reclaimer) Watermark() txid.T {
	return r.water
}

// Pending 返回堆中候选数。
func (r *Reclaimer) Pending() int {
	return len(r.h)
}

// Collect 弹出所有 Superseder < 当前水位的候选。
// 每考察一个堆顶（含使循环停止的那一次）examined 加一。
// 单次调用考察数 = 弹出数 + 1，与键总数无关。
func (r *Reclaimer) Collect() []Candidate {
	var out []Candidate
	for len(r.h) > 0 {
		r.examined++
		if r.h[0].Superseder >= r.water {
			break
		}
		out = append(out, heap.Pop(&r.h).(Candidate))
	}
	return out
}

// Examined 返回历次回收累计考察的版本数（只读，不重置）。
func (r *Reclaimer) Examined() int {
	return r.examined
}
