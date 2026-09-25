package dij

import (
	"container/heap"
	"errors"
	"math"
	"sync/atomic"

	"ontology/graph"
)

var (
	// ErrNegativeEdge is returned when an edge has a negative weight.
	ErrNegativeEdge = errors.New("dij: negative edge weight")
	// ErrBadSrc is returned when the source node is out of range.
	ErrBadSrc = errors.New("dij: source node out of range")
	// ErrBadEdge is returned when an edge endpoint is out of range.
	ErrBadEdge = errors.New("dij: edge endpoint out of range")
)

var relaxTotal atomic.Int64

// Relaxations reports relaxation attempts since process start. It is intended
// for diagnostics/tests; ShortestPath itself remains a pure function.
func Relaxations() int64 { return relaxTotal.Load() }

// ShortestPath computes shortest distances from src in a graph of n nodes.
func ShortestPath(n int, edges []graph.Edge, src int) ([]float64, error) {
	if src < 0 || src >= n {
		return nil, ErrBadSrc
	}
	for _, e := range edges {
		if e.From < 0 || e.From >= n || e.To < 0 || e.To >= n {
			return nil, ErrBadEdge
		}
	}
	g := graph.New(n)
	for _, e := range edges {
		if e.Weight < 0 || math.IsNaN(e.Weight) {
			return nil, ErrNegativeEdge
		}
		g.AddEdge(e.From, e.To, e.Weight)
	}

	dist := make([]float64, n)
	for i := range dist {
		dist[i] = math.Inf(1)
	}
	dist[src] = 0
	pq := &minHeap{{node: src, dist: 0}}

	for pq.Len() > 0 {
		cur := heap.Pop(pq).(entry)
		if cur.dist > dist[cur.node] { // stale record: lazy deletion
			continue
		}
		for _, e := range g.Neighbors(cur.node) {
			relaxTotal.Add(1)
			if nd := cur.dist + e.Weight; nd < dist[e.To] {
				dist[e.To] = nd
				heap.Push(pq, entry{node: e.To, dist: nd})
			}
		}
	}
	return dist, nil
}

type entry struct {
	node int
	dist float64
}

type minHeap []entry

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return h[i].dist < h[j].dist }
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)        { *h = append(*h, x.(entry)) }
func (h *minHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}
