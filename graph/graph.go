// Package graph 提供带权有向图的邻接表表示。依赖无。
package graph

// Edge 是一条带非负权重的有向边。
type Edge struct {
	U, V int
	W    float64
}

// Graph 是邻接表，只读共享是安全的（构建完成后不再修改）。
type Graph struct {
	adj [][]Edge
}

// New 返回 n 个节点的空图。
func New(n int) *Graph {
	return &Graph{adj: make([][]Edge, n)}
}

// AddEdge 添加一条 u -> v、权重为 w 的边。调用方需保证端点合法。
func (g *Graph) AddEdge(u, v int, w float64) {
	g.adj[u] = append(g.adj[u], Edge{U: u, V: v, W: w})
}

// N 返回节点数。
func (g *Graph) N() int { return len(g.adj) }

// Adj 返回 u 的出边切片（只读）。
func (g *Graph) Adj(u int) []Edge { return g.adj[u] }
