// Package reclaim 负责回收水位的推进与可回收候选的增量管理。
//
// 候选按遮蔽事务号（shadow）组织成最小堆：一个旧版本被提交事务号
// 为 shadow 的新版本遮蔽时入堆。回收时只从堆顶弹出 shadow < 水位
// 的候选，考察数量与可回收候选数量成正比，与键总数无关。
package reclaim

import (
	"container/heap"
	"sync"

	"ontology/snapshot"
	"ontology/txid"
)

// Candidate 是一个可回收候选：键 Key 上提交事务号为 Commit 的版本，
// 被提交事务号为 Shadow 的更新版本遮蔽。
type Candidate struct {
	Key    string
	Commit txid.ID
	Shadow txid.ID
}

// candidateHeap 是按 Shadow 升序的最小堆。
type candidateHeap []Candidate

func (h candidateHeap) Len() int           { return len(h) }
func (h candidateHeap) Less(i, j int) bool { return h[i].Shadow.Before(h[j].Shadow) }
func (h candidateHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *candidateHeap) Push(x any)        { *h = append(*h, x.(Candidate)) }
func (h *candidateHeap) Pop() any {
	old := *h
	n := len(old)
	c := old[n-1]
	*h = old[:n-1]
	return c
}

// Reclaimer 维护候选堆与只升不降的回收水位。
type Reclaimer struct {
	mu    sync.Mutex
	snaps *snapshot.Manager
	src   txid.Source
	heap  candidateHeap
	water txid.ID // 已发布水位，只升不降

	examined int64 // 非导出计数器：历次回收实际考察的版本数
}

// New 创建回收器。snaps 提供回收视野，src 用于无活跃快照时的水位取值。
func New(snaps *snapshot.Manager, src txid.Source) *Reclaimer {
	return &Reclaimer{snaps: snaps, src: src}
}

// Add 登记一个候选。由提交路径在新版本遮蔽旧版本时调用，O(log n)。
func (r *Reclaimer) Add(c Candidate) {
	r.mu.Lock()
	defer r.mu.Unlock()
	heap.Push(&r.heap, c)
}

// Collect 弹出一批可回收候选（shadow < 当前水位），并推进水位。
// 返回的候选仍需由调用方在版本链上执行安全移除。
// 考察数 = 弹出数，未就绪的堆顶只看不计。
func (r *Reclaimer) Collect() []Candidate {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, bounded := r.snaps.Horizon()
	effective := h
	if !bounded {
		effective = r.src.Current() // 无活跃快照：一切已提交皆安全
	}
	if effective.After(r.water) {
		r.water = effective
	}
	var out []Candidate
	for len(r.heap) > 0 {
		top := r.heap[0]
		if bounded && !top.Shadow.Before(h) {
			break
		}
		heap.Pop(&r.heap)
		r.examined++
		out = append(out, top)
	}
	return out
}

// Examined 返回历次回收实际考察的版本总数。
func (r *Reclaimer) Examined() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.examined
}

// WaterLevel 返回已发布的回收水位（只升不降）。
func (r *Reclaimer) WaterLevel() txid.ID {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.water
}

// Pending 返回堆中尚未回收的候选数（测试与诊断用）。
func (r *Reclaimer) Pending() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.heap)
}
