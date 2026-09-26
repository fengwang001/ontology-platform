// Package dag holds a directed graph with fixed node set [0,n) and validates
// every mutation; it depends on no other package in this module.
package dag

import "errors"

// Sentinel errors: callers decide with errors.Is. Each rejection is distinct.
var (
	ErrInvalidN   = errors.New("dag: n must be positive")
	ErrOutOfRange = errors.New("dag: edge references a node outside [0,n)")
	ErrSelfLoop   = errors.New("dag: self loops are forbidden")
	ErrDuplicate  = errors.New("dag: edge already exists")
	ErrCyclic     = errors.New("dag: graph contains a cycle")
)

// Edge is one directed edge U->V.
type Edge struct{ U, V int }

// Graph is an in-memory directed multiset-free graph. The zero value is not
// usable; construct it with New.
type Graph struct {
	n     int
	adj   [][]int
	edges map[[2]int]struct{}
}

// New creates a graph with nodes [0,n). n must be positive.
func New(n int) (*Graph, error) {
	if n <= 0 {
		return nil, ErrInvalidN
	}
	return &Graph{
		n:     n,
		adj:   make([][]int, n),
		edges: make(map[[2]int]struct{}),
	}, nil
}

// AddEdge registers u->v. It validates everything before touching state, so a
// rejected call leaves the graph unchanged.
func (g *Graph) AddEdge(u, v int) error {
	if u < 0 || u >= g.n || v < 0 || v >= g.n {
		return ErrOutOfRange
	}
	if u == v {
		return ErrSelfLoop
	}
	key := [2]int{u, v}
	if _, ok := g.edges[key]; ok {
		return ErrDuplicate
	}
	g.edges[key] = struct{}{}
	g.adj[u] = append(g.adj[u], v)
	return nil
}

// N reports the fixed node count.
func (g *Graph) N() int { return g.n }

// EdgeCount reports how many distinct edges have been accepted.
func (g *Graph) EdgeCount() int { return len(g.edges) }

// Edges returns a copy of every accepted edge.
func (g *Graph) Edges() []Edge {
	out := make([]Edge, 0, len(g.edges))
	for key := range g.edges {
		out = append(out, Edge{U: key[0], V: key[1]})
	}
	return out
}

// Acyclic reports whether the graph has no directed cycle, using a
// three-colour DFS.
func (g *Graph) Acyclic() bool {
	const white, gray, black = 0, 1, 2
	color := make([]byte, g.n)
	var visit func(int) bool
	visit = func(u int) bool {
		color[u] = gray
		for _, v := range g.adj[u] {
			if color[v] == gray || (color[v] == white && !visit(v)) {
				return false
			}
		}
		color[u] = black
		return true
	}
	for u := 0; u < g.n; u++ {
		if color[u] == white && !visit(u) {
			return false
		}
	}
	return true
}
