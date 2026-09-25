// Package dij 实现单源最短路径（Dijkstra + 最小堆 + lazy deletion）。依赖 graph。
package dij

import (
	"container/heap"
	"errors"
	"math"
	"sync/atomic"

	"ontology/graph"
)

var (
	ErrNegativeEdge = errors.New("dij: negative edge weight")
	ErrBadSrc       = errors.New("dij: source out of range")
	ErrBadEdge      = errors.New("dij: edge endpoint out of range")
)

var relaxations atomic.Int64

// Relaxations 返回累计松弛次数（仅供测试断言复杂度上界）。
func Relaxations() int64 { return relaxations.Load() }

// ShortestPath 返回 src 到各节点的最短距离，不可达为 +Inf。纯函数，可并发调用。
func ShortestPath(n int, edges []graph.Edge, src int) ([]float64, error) {
	if src < 0 || src >= n {
		return nil, ErrBadSrc
	}
	g := graph.New(n)
	for _, e := range edges {
		if e.U < 0 || e.U >= n || e.V < 0 || e.V >= n {
			return nil, ErrBadEdge
		}
		if e.W < 0 {
			return nil, ErrNegativeEdge
		}
		g.AddEdge(e.U, e.V, e.W)
	}
	dist := make([]float64, n)
	for i := range dist {
		dist[i] = math.Inf(1)
	}
	dist[src] = 0
	pq := &itemHeap{{node: src, dist: 0}}
	heap.Init(pq)
	for pq.Len() > 0 {
		it := heap.Pop(pq).(item)
		if it.dist > dist[it.node] {
			continue // 过期记录：lazy deletion，不得用它更新邻居
		}
		for _, e := range g.Adj(it.node) {
			relaxations.Add(1)
			if nd := it.dist + e.W; nd < dist[e.V] {
				dist[e.V] = nd
				heap.Push(pq, item{node: e.V, dist: nd})
			}
		}
	}
	return dist, nil
}

type item struct {
	node int
	dist float64
}

type itemHeap []item

func (h itemHeap) Len() int            { return len(h) }
func (h itemHeap) Less(i, j int) bool  { return h[i].dist < h[j].dist }
func (h itemHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *itemHeap) Push(x interface{}) { *h = append(*h, x.(item)) }
func (h *itemHeap) Pop() interface{} {
	old := *h
	it := old[len(old)-1]
	*h = old[:len(old)-1]
	return it
}
