// Package dij 实现非负权图的单源最短路径（Dijkstra，懒惰删除过期记录）。
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
	ErrBadSrc       = errors.New("dij: source node out of range")
	ErrBadEdge      = errors.New("dij: edge endpoint out of range")
)

var relaxations atomic.Int64

// Relaxations 返回累计松弛次数（供测试断言复杂度上界）。
func Relaxations() int64 { return relaxations.Load() }

type item struct {
	node int
	dist float64
}

type pq []item

func (p pq) Len() int           { return len(p) }
func (p pq) Less(i, j int) bool { return p[i].dist < p[j].dist }
func (p pq) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
func (p *pq) Push(x any)        { *p = append(*p, x.(item)) }

func (p *pq) Pop() any {
	old := *p
	x := old[len(old)-1]
	*p = old[:len(old)-1]
	return x
}

// ShortestPath 返回 src 到各节点的最短距离，不可达节点为 +Inf。
func ShortestPath(n int, edges []graph.Edge, src int) ([]float64, error) {
	if src < 0 || src >= n {
		return nil, ErrBadSrc
	}
	adj := make([][]graph.Edge, n)
	for _, e := range edges {
		if e.W < 0 {
			return nil, ErrNegativeEdge
		}
		if e.From < 0 || e.From >= n || e.To < 0 || e.To >= n {
			return nil, ErrBadEdge
		}
		adj[e.From] = append(adj[e.From], e)
	}
	dist := make([]float64, n)
	for i := range dist {
		dist[i] = math.Inf(1)
	}
	dist[src] = 0
	h := &pq{{node: src}}
	for h.Len() > 0 {
		cur := heap.Pop(h).(item)
		if cur.dist > dist[cur.node] {
			continue // 过期记录：已有更短距离定案，跳过
		}
		for _, e := range adj[cur.node] {
			relaxations.Add(1)
			if nd := cur.dist + e.W; nd < dist[e.To] {
				dist[e.To] = nd
				heap.Push(h, item{node: e.To, dist: nd})
			}
		}
	}
	return dist, nil
}
