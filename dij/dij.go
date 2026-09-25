package dij

import (
	"container/heap"
	"errors"
	"math"

	"ontology/graph"
)

var (
	// ErrNegativeEdge 表示存在权重为负的边。
	ErrNegativeEdge = errors.New("dij: negative edge weight")
	// ErrBadSrc 表示源点编号越界。
	ErrBadSrc = errors.New("dij: source node out of range")
	// ErrBadEdge 表示边端点编号越界。
	ErrBadEdge = errors.New("dij: edge endpoint out of range")
)

// ShortestPath 计算从 src 到全部节点的最短距离，不可达为 +Inf。
func ShortestPath(n int, edges []graph.Edge, src int) ([]float64, error) {
	dist, _, err := shortestPath(n, edges, src)
	return dist, err
}

// ShortestPathWithCount 额外返回松弛次数（成功松弛计数 ≤ 处理的边数 ≤ m）。
func ShortestPathWithCount(n int, edges []graph.Edge, src int) ([]float64, int, error) {
	return shortestPath(n, edges, src)
}

type pqItem struct {
	node int
	dist float64
}

type pqHeap []pqItem

func (h pqHeap) Len() int           { return len(h) }
func (h pqHeap) Less(i, j int) bool { return h[i].dist < h[j].dist }
func (h pqHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *pqHeap) Push(x any)        { *h = append(*h, x.(pqItem)) }
func (h *pqHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

func shortestPath(n int, edges []graph.Edge, src int) ([]float64, int, error) {
	if n < 0 || src < 0 || src >= n {
		return nil, 0, ErrBadSrc
	}
	for _, e := range edges {
		if e.U < 0 || e.U >= n || e.V < 0 || e.V >= n {
			return nil, 0, ErrBadEdge
		}
		if e.W < 0 {
			return nil, 0, ErrNegativeEdge
		}
	}

	g := graph.Build(n, edges)
	dist := make([]float64, n)
	for i := range dist {
		dist[i] = math.Inf(1)
	}
	dist[src] = 0

	q := &pqHeap{{node: src, dist: 0}}
	heap.Init(q)
	relaxes := 0

	for q.Len() > 0 {
		cur := heap.Pop(q).(pqItem)
		if cur.dist > dist[cur.node] {
			continue // 过期记录：该节点已在更短距离上确定，惰性删除
		}
		for _, e := range g.From(cur.node) {
			relaxes++
			nd := cur.dist + e.W
			if nd < dist[e.V] {
				dist[e.V] = nd
				heap.Push(q, pqItem{node: e.V, dist: nd})
			}
		}
	}
	return dist, relaxes, nil
}
