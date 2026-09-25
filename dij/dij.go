package dij

import (
	"container/heap"
	"errors"
	"math"
	"sync/atomic"

	"ontology/graph"
)

var (
	ErrNegativeEdge = errors.New("negative edge")
	ErrBadSrc       = errors.New("source node out of range")
	ErrBadEdge      = errors.New("edge endpoint out of range")
)

var relaxations atomic.Uint64

type queueItem struct {
	node int
	dist float64
}

type minQueue []queueItem

func (q minQueue) Len() int           { return len(q) }
func (q minQueue) Less(i, j int) bool { return q[i].dist < q[j].dist }
func (q minQueue) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }

func (q *minQueue) Push(value any) {
	*q = append(*q, value.(queueItem))
}

func (q *minQueue) Pop() any {
	old := *q
	last := len(old) - 1
	item := old[last]
	*q = old[:last]
	return item
}

func Relaxations() uint64 { return relaxations.Load() }

func ResetRelaxations() { relaxations.Store(0) }

func ShortestPath(n int, edges []graph.Edge, src int) ([]float64, error) {
	if src < 0 || src >= n {
		return nil, ErrBadSrc
	}

	g := graph.New(n)
	for _, edge := range edges {
		if edge.Weight < 0 {
			return nil, ErrNegativeEdge
		}
		if !g.AddEdge(edge.From, edge.To, edge.Weight) {
			return nil, ErrBadEdge
		}
	}

	dist := make([]float64, n)
	for node := range dist {
		dist[node] = math.Inf(1)
	}
	dist[src] = 0

	queue := &minQueue{{node: src, dist: 0}}
	heap.Init(queue)

	for queue.Len() > 0 {
		current := heap.Pop(queue).(queueItem)
		if current.dist > dist[current.node] {
			continue
		}

		node := current.node
		g.EachNeighbor(node, func(next int, weight float64) bool {
			relaxations.Add(1)
			candidate := current.dist + weight
			if candidate < dist[next] {
				dist[next] = candidate
				heap.Push(queue, queueItem{node: next, dist: candidate})
			}
			return true
		})
	}

	return dist, nil
}
