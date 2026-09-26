// Package dag 维护有向图结构：节点入度、出边邻接表，以及加边时的合法性校验。
// 不依赖其他包。
package dag

import "errors"

// 可判定的哨兵错误，四者互不相同。
var (
	ErrSelfLoop  = errors.New("dag: self loop")
	ErrNodeRange = errors.New("dag: node out of range")
	ErrDupEdge   = errors.New("dag: duplicate edge")
	ErrCycle     = errors.New("dag: graph contains a cycle")
)

// Graph 是节点编号 [0, n) 的有向图，只登记边，不做排序。
type Graph struct {
	n     int
	indeg []int
	adj   [][]int
	seen  map[[2]int]bool
	m     int
}

// New 创建 n 个节点的空图，n 在此固定。
func New(n int) *Graph {
	return &Graph{
		n:     n,
		indeg: make([]int, n),
		adj:   make([][]int, n),
		seen:  make(map[[2]int]bool),
	}
}

// AddEdge 登记有向边 u→v。自环、越界节点、重复边都会被拒绝；
// 全部校验通过后才改动状态，被拒时图保持不变。
func (g *Graph) AddEdge(u, v int) error {
	if u == v {
		return ErrSelfLoop
	}
	if u < 0 || u >= g.n || v < 0 || v >= g.n {
		return ErrNodeRange
	}
	if g.seen[[2]int{u, v}] {
		return ErrDupEdge
	}
	g.seen[[2]int{u, v}] = true
	g.adj[u] = append(g.adj[u], v)
	g.indeg[v]++
	g.m++
	return nil
}

// N 返回节点数。
func (g *Graph) N() int { return g.n }

// EdgeCount 返回已登记的边数。
func (g *Graph) EdgeCount() int { return g.m }

// Indeg 返回节点 v 的当前入度。
func (g *Graph) Indeg(v int) int { return g.indeg[v] }

// Adj 返回节点 u 的出边终点列表。
func (g *Graph) Adj(u int) []int { return g.adj[u] }
