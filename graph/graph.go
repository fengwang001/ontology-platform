// Package graph 存储带非负权重的属性依赖图（邻接表）。
package graph

// Edge 是一条从 U 到 V、权重为 W 的有向边。
type Edge struct {
	U, V int
	W    float64
}

type edgeItem struct {
	to int
	w  float64
}

// Graph 是进程内存中的有向加权图，零值不可用，请用 New 构造。
type Graph struct {
	n     int
	edges []Edge
	adj   [][]edgeItem
}

// New 创建含 n 个节点（编号 0..n-1）的空图。
func New(n int) *Graph {
	return &Graph{n: n, adj: make([][]edgeItem, n)}
}

// AddEdge 添加一条有向边 u -> v，权重 w，不做任何校验。
func (g *Graph) AddEdge(u, v int, w float64) {
	e := Edge{U: u, V: v, W: w}
	g.edges = append(g.edges, e)
	g.adj[u] = append(g.adj[u], edgeItem{to: v, w: w})
}

// N 返回节点数。
func (g *Graph) N() int { return g.n }

// Edges 返回添加边时的原始顺序快照。
func (g *Graph) Edges() []Edge {
	out := make([]Edge, len(g.edges))
	copy(out, g.edges)
	return out
}

// Neighbors 返回 u 的出边（to, weight），顺序与添加顺序一致。
func (g *Graph) Neighbors(u int) []Edge {
	items := g.adj[u]
	out := make([]Edge, 0, len(items))
	for _, it := range items {
		out = append(out, Edge{U: u, V: it.to, W: it.w})
	}
	return out
}
