// Package graph provides a weighted directed graph as an adjacency list.
package graph

// Edge is a directed edge From -> To with non-negative weight W.
type Edge struct {
	From, To int
	W        float64
}

// Graph is an adjacency list over nodes 0..n-1.
type Graph struct {
	adj [][]Edge
}

// New returns an empty graph with n nodes.
func New(n int) *Graph { return &Graph{adj: make([][]Edge, n)} }

// AddEdge appends the directed edge u -> v with weight w.
func (g *Graph) AddEdge(u, v int, w float64) {
	g.adj[u] = append(g.adj[u], Edge{From: u, To: v, W: w})
}

// Adj returns the outgoing edges of u.
func (g *Graph) Adj(u int) []Edge { return g.adj[u] }
