// Package wg implements a weighted undirected graph with validated edge
// insertion. It has no dependencies on other packages of this module.
package wg

import "errors"

// Sentinel errors, each distinguishing one rejected operation.
var (
	ErrTooFewNodes       = errors.New("wg: need at least 2 nodes")
	ErrNodeOutOfRange    = errors.New("wg: node out of range [0,n)")
	ErrSelfLoop          = errors.New("wg: self loop")
	ErrDuplicateEdge     = errors.New("wg: duplicate edge")
	ErrNonPositiveWeight = errors.New("wg: weight must be positive")
)

// Graph is a weighted undirected graph on nodes 0..n-1.
type Graph struct {
	n     int
	adj   []map[int]int64
	edges int
}

// New creates a graph with n nodes. n < 2 is rejected.
func New(n int) (*Graph, error) {
	if n < 2 {
		return nil, ErrTooFewNodes
	}
	return &Graph{n: n, adj: make([]map[int]int64, n)}, nil
}

// AddEdge inserts the undirected edge (u,v) with positive weight w.
// Every check runs before any write, so a rejected call leaves the
// graph untouched.
func (g *Graph) AddEdge(u, v int, w int64) error {
	if u < 0 || u >= g.n || v < 0 || v >= g.n {
		return ErrNodeOutOfRange
	}
	if u == v {
		return ErrSelfLoop
	}
	if w <= 0 {
		return ErrNonPositiveWeight
	}
	if _, ok := g.adj[u][v]; ok {
		return ErrDuplicateEdge
	}
	if g.adj[u] == nil {
		g.adj[u] = map[int]int64{}
	}
	if g.adj[v] == nil {
		g.adj[v] = map[int]int64{}
	}
	g.adj[u][v] = w
	g.adj[v][u] = w
	g.edges++
	return nil
}

// N returns the number of nodes.
func (g *Graph) N() int { return g.n }

// EdgeCount returns the number of registered edges.
func (g *Graph) EdgeCount() int { return g.edges }

// Adjacency returns a deep copy of the adjacency maps; callers may
// mutate the result freely (the Stoer-Wagner core contracts nodes in
// its own copy, keeping reads concurrency-safe).
func (g *Graph) Adjacency() []map[int]int64 {
	out := make([]map[int]int64, g.n)
	for i, m := range g.adj {
		if m == nil {
			continue
		}
		c := make(map[int]int64, len(m))
		for k, v := range m {
			c[k] = v
		}
		out[i] = c
	}
	return out
}
