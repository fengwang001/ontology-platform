package graph

// Edge describes one directed, weighted edge of the dependency graph.
type Edge struct {
	From   int
	To     int
	Weight float64
}

// Graph is a directed graph represented by weighted adjacency lists.
type Graph struct {
	adj [][]Edge
}

// New creates a graph with n nodes numbered from 0 through n-1.
func New(n int) *Graph {
	return &Graph{adj: make([][]Edge, n)}
}

// AddEdge adds a directed edge from u to v with weight w.
func (g *Graph) AddEdge(u, v int, w float64) {
	g.adj[u] = append(g.adj[u], Edge{From: u, To: v, Weight: w})
}

// Neighbors returns the outgoing edges from u in insertion order.
func (g *Graph) Neighbors(u int) []Edge {
	return g.adj[u]
}

// Nodes returns the number of nodes.
func (g *Graph) Nodes() int {
	return len(g.adj)
}
