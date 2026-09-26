// Package wdag 提供带权有向无环图的结构：邻接、加边校验与环检测。
// 不依赖本工程其他包。
package wdag

import "errors"

// 可判定的哨兵错误，互不相同。
var (
	ErrBadN      = errors.New("wdag: 节点数非正")
	ErrNodeRange = errors.New("wdag: 边引用 [0,n) 外的节点")
	ErrSelfLoop  = errors.New("wdag: 自环")
	ErrDupEdge   = errors.New("wdag: 重复边")
	ErrCycle     = errors.New("wdag: 图含环")
)

// Edge 是一条有向边 From→To，权 W（可为负）。
type Edge struct {
	From, To int
	W        int64
}

// Graph 是节点 0..n-1 上的带权有向图。加边外的访问方法只读。
type Graph struct {
	n     int
	out   [][]Edge
	in    [][]Edge
	seen  map[[2]int]bool
	edges int
}

// New 建 n 个节点的空图；n 非正返回 ErrBadN。
func New(n int) (*Graph, error) {
	if n <= 0 {
		return nil, ErrBadN
	}
	return &Graph{n: n, out: make([][]Edge, n), in: make([][]Edge, n), seen: make(map[[2]int]bool)}, nil
}

// AddEdge 加边 u→v 权 w。任何校验失败都不改变图状态。
func (g *Graph) AddEdge(u, v int, w int64) error {
	if u < 0 || u >= g.n || v < 0 || v >= g.n {
		return ErrNodeRange
	}
	if u == v {
		return ErrSelfLoop
	}
	if g.seen[[2]int{u, v}] {
		return ErrDupEdge
	}
	g.seen[[2]int{u, v}] = true
	g.out[u] = append(g.out[u], Edge{From: u, To: v, W: w})
	g.in[v] = append(g.in[v], Edge{From: u, To: v, W: w})
	g.edges++
	return nil
}

// N 返回节点数。
func (g *Graph) N() int { return g.n }

// EdgeCount 返回已登记的边数。
func (g *Graph) EdgeCount() int { return g.edges }

// OutEdges 返回 u 的出边（只读，调用方不得修改）。
func (g *Graph) OutEdges(u int) []Edge { return g.out[u] }

// InEdges 返回 v 的入边（只读，调用方不得修改）。
func (g *Graph) InEdges(v int) []Edge { return g.in[v] }

// Topo 返回一个拓扑序；图含环时返回 ErrCycle。
func (g *Graph) Topo() ([]int, error) {
	indeg := make([]int, g.n)
	for v := range g.n {
		indeg[v] = len(g.in[v])
	}
	queue := make([]int, 0, g.n)
	for v := range g.n {
		if indeg[v] == 0 {
			queue = append(queue, v)
		}
	}
	order := make([]int, 0, g.n)
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		order = append(order, u)
		for _, e := range g.out[u] {
			indeg[e.To]--
			if indeg[e.To] == 0 {
				queue = append(queue, e.To)
			}
		}
	}
	if len(order) != g.n {
		return nil, ErrCycle
	}
	return order, nil
}
