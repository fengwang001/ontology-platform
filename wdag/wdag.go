// Package wdag 提供带权有向图结构：邻接、加边校验、环检测。不依赖其他包。
package wdag

import "errors"

// 可判定哨兵错误，互不相同。
var (
	ErrBadOrder       = errors.New("wdag: node count must be positive")
	ErrNodeOutOfRange = errors.New("wdag: edge references undefined node")
	ErrSelfLoop       = errors.New("wdag: self loop not allowed")
	ErrDuplicateEdge  = errors.New("wdag: duplicate edge")
	ErrCycle          = errors.New("wdag: graph contains a cycle")
)

// Edge 是一条出边/入边：From→To，权 W。
type Edge struct {
	From, To int
	W        int64
}

// Graph 是节点 0..n-1 上的带权有向图，邻接表存储。
type Graph struct {
	n     int
	out   [][]Edge
	in    [][]Edge
	seen  map[[2]int]struct{}
	nedge int
}

// New 建 n 个节点的空图；n 非正返回 ErrBadOrder。
func New(n int) (*Graph, error) {
	if n <= 0 {
		return nil, ErrBadOrder
	}
	return &Graph{n: n, out: make([][]Edge, n), in: make([][]Edge, n), seen: make(map[[2]int]struct{})}, nil
}

// AddEdge 加有向边 u→v 权 w。非法时整体失败、状态不变。
func (g *Graph) AddEdge(u, v int, w int64) error {
	if u < 0 || u >= g.n || v < 0 || v >= g.n {
		return ErrNodeOutOfRange
	}
	if u == v {
		return ErrSelfLoop
	}
	key := [2]int{u, v}
	if _, ok := g.seen[key]; ok {
		return ErrDuplicateEdge
	}
	g.out[u] = append(g.out[u], Edge{From: u, To: v, W: w})
	g.in[v] = append(g.in[v], Edge{From: u, To: v, W: w})
	g.seen[key] = struct{}{}
	g.nedge++
	return nil
}

// N 返回节点数。
func (g *Graph) N() int { return g.n }

// EdgeCount 返回已登记边数。
func (g *Graph) EdgeCount() int { return g.nedge }

// Out 返回 u 的出边切片（只读共用，调用方不得修改）。
func (g *Graph) Out(u int) []Edge { return g.out[u] }

// In 返回 v 的入边切片（只读共用，调用方不得修改）。
func (g *Graph) In(v int) []Edge { return g.in[v] }

// HasCycle 用 Kahn 算法检测环：能处理完全部节点则无环。
func (g *Graph) HasCycle() bool {
	indeg := make([]int, g.n)
	for v := range g.in {
		indeg[v] = len(g.in[v])
	}
	queue := make([]int, 0, g.n)
	for v, d := range indeg {
		if d == 0 {
			queue = append(queue, v)
		}
	}
	done := 0
	for len(queue) > 0 {
		u := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		done++
		for _, e := range g.out[u] {
			indeg[e.To]--
			if indeg[e.To] == 0 {
				queue = append(queue, e.To)
			}
		}
	}
	return done < g.n
}
