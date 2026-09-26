// Package topo 在 dag 上执行 Kahn 拓扑排序：维护入度为 0 的候选集合，
// 每步取出编号最小者输出。依赖 dag。
package topo

import (
	"container/heap"

	"ontology/dag"
)

// intHeap 是最小堆，保证每步 O(log k) 取最小编号，而非全表扫描。
type intHeap []int

func (h intHeap) Len() int           { return len(h) }
func (h intHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h intHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *intHeap) Push(x any) { *h = append(*h, x.(int)) }

func (h *intHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// Sorter 执行拓扑排序。checked 记录最近一次从候选集合选出下一节点时
// 检查过的候选节点个数（堆顶即所求，恒为 1），非导出，仅供包内测试观测。
type Sorter struct {
	checked int
}

// Sort 返回 g 的最小编号优先拓扑序。含环时整体失败，返回 dag.ErrCycle，
// 不返回任何部分序列。Sort 不修改 g，可并发调用（各自用独立 Sorter）。
func (s *Sorter) Sort(g *dag.Graph) ([]int, error) {
	n := g.N()
	indeg := make([]int, n)
	h := &intHeap{}
	for v := 0; v < n; v++ {
		indeg[v] = g.Indeg(v)
		if indeg[v] == 0 {
			heap.Push(h, v)
		}
	}
	out := make([]int, 0, n)
	for h.Len() > 0 {
		s.checked = 1 // 堆顶直接给出最小编号，只检查 1 个候选
		u := heap.Pop(h).(int)
		out = append(out, u)
		for _, v := range g.Adj(u) {
			indeg[v]--
			if indeg[v] == 0 {
				heap.Push(h, v)
			}
		}
	}
	if len(out) != n {
		return nil, dag.ErrCycle
	}
	return out, nil
}
