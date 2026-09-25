// Package graph 提供带权有向图的邻接表表示。
package graph

// Edge 是一条有向带权边。
type Edge struct {
	From, To int
	W        float64
}

// Graph 是邻接表表示的有向带权图。
type Graph struct {
	adj [][]Edge
}

// New 返回含 n 个节点的空图。
func New(n int) *Graph { return &Graph{adj: make([][]Edge, n)} }

// AddEdge 加入边 u -> v，权重为 w。
func (g *Graph) AddEdge(u, v int, w float64) {
	g.adj[u] = append(g.adj[u], Edge{From: u, To: v, W: w})
}

// Edges 返回图中全部边。
func (g *Graph) Edges() []Edge {
	var out []Edge
	for _, es := range g.adj {
		out = append(out, es...)
	}
	return out
}
