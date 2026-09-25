// Package dij computes single-source shortest paths with Dijkstra's algorithm.
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

var relaxCount atomic.Int64

// Relaxations returns the total number of edge relaxations performed so far.
func Relaxations() int64 { return relaxCount.Load() }

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
	it := old[len(old)-1]
	*p = old[:len(old)-1]
	return it
}

// ShortestPath returns the shortest distances from src over n nodes.
// Unreachable nodes get +Inf. It is pure and safe for concurrent use.
func ShortestPath(n int, edges []graph.Edge, src int) ([]float64, error) {
	if src < 0 || src >= n {
		return nil, ErrBadSrc
	}
	g := graph.New(n)
	for _, e := range edges {
		if e.From < 0 || e.From >= n || e.To < 0 || e.To >= n {
			return nil, ErrBadEdge
		}
		if e.W < 0 {
			return nil, ErrNegativeEdge
		}
		g.AddEdge(e.From, e.To, e.W)
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
			continue // stale entry: lazy deletion, never relax from it
		}
		for _, e := range g.Adj(cur.node) {
			relaxCount.Add(1)
			if nd := cur.dist + e.W; nd < dist[e.To] {
				dist[e.To] = nd
				heap.Push(h, item{node: e.To, dist: nd})
			}
		}
	}
	return dist, nil
}
