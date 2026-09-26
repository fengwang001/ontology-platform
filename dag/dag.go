// Package dag holds a directed graph that is required to be acyclic.
// It depends on no other package in this module.
package dag

import (
	"errors"
	"sync"
)

// Sentinel errors. All validation failures are decidable via errors.Is and are
// pairwise distinct.
var (
	ErrInvalidN       = errors.New("dag: node count must be positive")
	ErrNodeOutOfRange = errors.New("dag: edge references a node outside [0, n)")
	ErrSelfLoop       = errors.New("dag: self loops are not allowed")
	ErrDuplicateEdge  = errors.New("dag: edge already exists")
	ErrCycle          = errors.New("dag: graph contains a cycle")
)

// DAG is an in-memory directed graph with fixed node set [0, n).
type DAG struct {
	mu    sync.RWMutex
	n     int
	adj   [][]int
	edges map[[2]int]struct{}
}

// New creates a graph with n isolated nodes. It fails without retaining state
// when n is not positive.
func New(n int) (*DAG, error) {
	if n <= 0 {
		return nil, ErrInvalidN
	}
	return &DAG{
		n:     n,
		adj:   make([][]int, n),
		edges: make(map[[2]int]struct{}),
	}, nil
}

// N reports the fixed node count.
func (g *DAG) N() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.n
}

// AddEdge registers the directed edge u->v. Checks run before any mutation, so
// a rejected call leaves the graph unchanged.
func (g *DAG) AddEdge(u, v int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if u < 0 || u >= g.n || v < 0 || v >= g.n {
		return ErrNodeOutOfRange
	}
	if u == v {
		return ErrSelfLoop
	}
	key := [2]int{u, v}
	if _, ok := g.edges[key]; ok {
		return ErrDuplicateEdge
	}
	g.edges[key] = struct{}{}
	g.adj[u] = append(g.adj[u], v)
	return nil
}

// EdgeCount reports how many distinct edges have been accepted.
func (g *DAG) EdgeCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.edges)
}

// Snapshot returns the node count and a deep copy of the adjacency lists,
// safe to use without holding the graph lock.
func (g *DAG) Snapshot() (int, [][]int) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([][]int, g.n)
	for u := range g.adj {
		out[u] = append(out[u], g.adj[u]...)
	}
	return g.n, out
}

// HasCycle runs Kahn's algorithm and reports whether the current graph is not a
// DAG (including any self loop, though AddEdge rejects those).
func (g *DAG) HasCycle() bool {
	n, adj := g.Snapshot()
	indeg := make([]int, n)
	for u := range adj {
		for _, v := range adj[u] {
			indeg[v]++
		}
	}
	queue := make([]int, 0, n)
	for v := 0; v < n; v++ {
		if indeg[v] == 0 {
			queue = append(queue, v)
		}
	}
	seen := 0
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		seen++
		for _, v := range adj[u] {
			indeg[v]--
			if indeg[v] == 0 {
				queue = append(queue, v)
			}
		}
	}
	return seen != n
}
