// Package graph is a minimal adjacency-list directed graph.
package graph

// Graph is a directed graph over nodes 0..n-1.
type Graph struct {
	adj [][]int
}

// New returns an empty graph with n nodes.
func New(n int) *Graph { return &Graph{adj: make([][]int, n)} }

// AddEdge appends a directed edge u -> v.
func (g *Graph) AddEdge(u, v int) { g.adj[u] = append(g.adj[u], v) }

// Adj returns the out-neighbors of u in insertion order.
func (g *Graph) Adj(u int) []int { return g.adj[u] }
