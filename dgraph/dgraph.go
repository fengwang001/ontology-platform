// Package dgraph is a directed graph on nodes [0, n) rooted at node 0.
package dgraph

import "errors"

var (
	ErrNonPositiveN   = errors.New("dgraph: node count must be positive")
	ErrNodeOutOfRange = errors.New("dgraph: edge references undefined node")
	ErrSelfLoop       = errors.New("dgraph: self loop not allowed")
	ErrDuplicateEdge  = errors.New("dgraph: duplicate edge")
)

// Graph is an adjacency-list directed graph with a fixed node count.
type Graph struct {
	n     int
	adj   [][]int
	seen  map[[2]int]struct{}
	edges int
}

// New creates a graph with n nodes; n must be positive.
func New(n int) (*Graph, error) {
	if n <= 0 {
		return nil, ErrNonPositiveN
	}
	return &Graph{n: n, adj: make([][]int, n), seen: make(map[[2]int]struct{})}, nil
}

// AddEdge adds the directed edge u->v. Rejected edges leave the graph unchanged.
func (g *Graph) AddEdge(u, v int) error {
	if u < 0 || u >= g.n || v < 0 || v >= g.n {
		return ErrNodeOutOfRange
	}
	if u == v {
		return ErrSelfLoop
	}
	k := [2]int{u, v}
	if _, ok := g.seen[k]; ok {
		return ErrDuplicateEdge
	}
	g.seen[k] = struct{}{}
	g.adj[u] = append(g.adj[u], v)
	g.edges++
	return nil
}

// N returns the node count.
func (g *Graph) N() int { return g.n }

// EdgeCount returns the number of accepted edges.
func (g *Graph) EdgeCount() int { return g.edges }

// Succ returns the successors of u.
func (g *Graph) Succ(u int) []int { return g.adj[u] }
