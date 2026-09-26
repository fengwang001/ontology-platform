// Package topo 在 dag.Graph 的快照上执行 Kahn 拓扑排序，
// 每步用最小堆取编号最小的入度为 0 节点。依赖 dag。
package topo

import (
	"container/heap"
	"errors"

	"ontology/dag"
)

// ErrCycle 表示图含环：仍有未输出节点但已无入度为 0 的节点。
var ErrCycle = errors.New("topo: graph contains a cycle")

// minHeap 按编号取最小的有序结构（堆），避免每步全表扫描。
type minHeap []int

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)        { *h = append(*h, x.(int)) }
func (h *minHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// sorter 执行一次排序。checked 是非导出计数器：最近一次从候选集合
// 选出下一节点时检查过的候选节点个数；maxChecked 是其逐步最大值。
// 两者都不出现于任何公开接口。
type sorter struct {
	checked    int
	maxChecked int
}

// Sort 返回 g 的最小编号优先拓扑序；含环时返回 ErrCycle 且不返回任何序列。
func Sort(g *dag.Graph) ([]int, error) {
	indeg, adj := g.Snapshot()
	s := &sorter{}
	return s.run(indeg, adj)
}

func (s *sorter) run(indeg []int, adj [][]int) ([]int, error) {
	h := &minHeap{}
	for v, d := range indeg {
		if d == 0 {
			heap.Push(h, v)
		}
	}
	order := make([]int, 0, len(indeg))
	for h.Len() > 0 {
		// 选下一节点：只检查堆顶这 1 个候选，不扫描其余节点。
		s.checked = 1
		if s.checked > s.maxChecked {
			s.maxChecked = s.checked
		}
		v := heap.Pop(h).(int)
		order = append(order, v)
		for _, w := range adj[v] {
			indeg[w]--
			if indeg[w] == 0 {
				heap.Push(h, w)
			}
		}
	}
	if len(order) != len(indeg) {
		return nil, ErrCycle // 整体失败，不返回任何部分序列
	}
	return order, nil
}
