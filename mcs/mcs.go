// Package mcs 用最大基数搜索（MCS）求完美消除序并判定弦图。依赖 ug。
package mcs

import (
	"container/heap"

	"ontology/ug"
)

// Result 是一次 Compute 的结果。
type Result struct {
	PEO      []int // MCS 选中顺序的逆序
	Chordal  bool  // 是否弦图
	Violator int   // PEO 中最靠前的 N+(v) 不成团的节点；弦图为 -1
}

// Solver 执行 MCS。checked 是非导出计数器：最近一次选出下一节点时
// 检查过的候选节点个数（堆顶一次取最大，恒为 1），不出现于公开接口。
type Solver struct {
	checked int
}

type item struct{ node, weight int }

// maxHeap 按 (权重降序, 编号升序) 组织的最大堆，pos 支持 O(log n) 增权。
type maxHeap struct {
	items []item
	pos   []int
}

func (h *maxHeap) Len() int { return len(h.items) }
func (h *maxHeap) Less(i, j int) bool {
	a, b := h.items[i], h.items[j]
	if a.weight != b.weight {
		return a.weight > b.weight
	}
	return a.node < b.node
}
func (h *maxHeap) Swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.pos[h.items[i].node] = i
	h.pos[h.items[j].node] = j
}
func (h *maxHeap) Push(x any) {
	it := x.(item)
	h.pos[it.node] = len(h.items)
	h.items = append(h.items, it)
}
func (h *maxHeap) Pop() any {
	old := h.items
	n := len(old)
	it := old[n-1]
	h.pos[it.node] = -1
	h.items = old[:n-1]
	return it
}

// Compute 跑 MCS 得到 PEO，再逐节点检查「后续邻居成团」。
func (s *Solver) Compute(g *ug.Graph) Result {
	n := g.N()
	h := &maxHeap{pos: make([]int, n)}
	for i := 0; i < n; i++ {
		h.items = append(h.items, item{node: i})
		h.pos[i] = i
	}
	heap.Init(h)
	removed := make([]bool, n)
	order := make([]int, 0, n)
	for h.Len() > 0 {
		s.checked = 1 // 只检查堆顶这一个候选即得最大者
		v := heap.Pop(h).(item).node
		removed[v] = true
		order = append(order, v)
		for _, u := range g.Neighbors(v) {
			if removed[u] {
				continue
			}
			h.items[h.pos[u]].weight++
			heap.Fix(h, h.pos[u])
		}
	}
	peo := make([]int, n)
	for i, v := range order {
		peo[n-1-i] = v
	}
	violator := firstViolator(g, peo)
	return Result{PEO: peo, Chordal: violator == -1, Violator: violator}
}

// firstViolator 返回 PEO 中最靠前的、N+(v) 不成团的节点；全成团返回 -1。
func firstViolator(g *ug.Graph, peo []int) int {
	rank := make([]int, len(peo))
	for i, v := range peo {
		rank[v] = i
	}
	for _, v := range peo {
		var later []int
		for _, u := range g.Neighbors(v) {
			if rank[u] > rank[v] {
				later = append(later, u)
			}
		}
		if !clique(g, later) {
			return v
		}
	}
	return -1
}

// clique 报告 ns 中任意两节点之间是否都有边。
func clique(g *ug.Graph, ns []int) bool {
	for i := 0; i < len(ns); i++ {
		for j := i + 1; j < len(ns); j++ {
			if !g.HasEdge(ns[i], ns[j]) {
				return false
			}
		}
	}
	return true
}
