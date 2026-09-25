package graph

// Edge 是属性依赖图中的一条有向边，W 为非负权重。
type Edge struct {
	U int
	V int
	W float64
}

// Graph 是固定节点数的带权有向图，采用邻接表存储。
type Graph struct {
	n     int
	edges []Edge
	adj   [][]Edge
}

// New 创建含 n 个节点（编号 0..n-1）的空图。
func New(n int) *Graph {
	return &Graph{n: n, adj: make([][]Edge, n)}
}

// AddEdge 添加一条从 u 到 v、权重为 w 的有向边（平行边允许）。
func (g *Graph) AddEdge(u, v int, w float64) {
	e := Edge{U: u, V: v, W: w}
	g.edges = append(g.edges, e)
	g.adj[u] = append(g.adj[u], e)
}

// N 返回节点数。
func (g *Graph) N() int { return g.n }

// Edges 返回全部边（只读用途）。
func (g *Graph) Edges() []Edge { return g.edges }

// From 返回从 u 出发的全部出边（只读用途）。
func (g *Graph) From(u int) []Edge { return g.adj[u] }

// Build 用给定边集构建图，不做合法性校验（由上层保证）。
func Build(n int, edges []Edge) *Graph {
	g := New(n)
	for _, e := range edges {
		g.AddEdge(e.U, e.V, e.W)
	}
	return g
}
