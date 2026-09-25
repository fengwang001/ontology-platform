// Package graph 提供有向图的邻接表表示。
package graph

// Graph 是 n 个节点（编号 0..n-1）的有向图邻接表。
type Graph struct {
	adj [][]int
	m   int
}

// New 创建 n 个节点的空图。
func New(n int) *Graph {
	if n < 0 {
		n = 0
	}
	return &Graph{adj: make([][]int, n)}
}

// AddEdge 添加有向边 u→v，按添加顺序保留以保证遍历确定性。
func (g *Graph) AddEdge(u, v int) {
	g.adj[u] = append(g.adj[u], v)
	g.m++
}

// Nodes 返回节点数。
func (g *Graph) Nodes() int { return len(g.adj) }

// Edges 返回边数。
func (g *Graph) Edges() int { return g.m }

// Out 返回 u 的出边终点切片（按添加顺序）。
func (g *Graph) Out(u int) []int { return g.adj[u] }
