// Package dg 提供有向图结构：邻接、加边与非法边校验。不依赖其他包。
package dg

import "errors"

// 可判定的哨兵错误，四者互不相同。
var (
	ErrNonPositiveN   = errors.New("dg: node count must be positive")
	ErrNodeOutOfRange = errors.New("dg: edge references undefined node")
	ErrSelfLoop       = errors.New("dg: self loop not allowed")
	ErrDuplicateEdge  = errors.New("dg: duplicate edge")
)

// Graph 是节点编号 [0,n) 的有向图，邻接用布尔矩阵表示。
type Graph struct {
	n     int
	adj   [][]bool
	edges int
}

// New 创建 n 个节点的空图；n 非正时整体失败，不留下任何状态。
func New(n int) (*Graph, error) {
	if n <= 0 {
		return nil, ErrNonPositiveN
	}
	adj := make([][]bool, n)
	for i := range adj {
		adj[i] = make([]bool, n)
	}
	return &Graph{n: n, adj: adj}, nil
}

// AddEdge 登记有向边 u→v。先完成全部校验再写邻接：
// 任何校验失败都不改变图状态（失败不留痕）。
func (g *Graph) AddEdge(u, v int) error {
	if u < 0 || u >= g.n || v < 0 || v >= g.n {
		return ErrNodeOutOfRange
	}
	if u == v {
		return ErrSelfLoop
	}
	if g.adj[u][v] {
		return ErrDuplicateEdge
	}
	g.adj[u][v] = true
	g.edges++
	return nil
}

// N 返回节点个数。
func (g *Graph) N() int { return g.n }

// EdgeCount 返回已登记的边数。
func (g *Graph) EdgeCount() int { return g.edges }

// HasEdge 报告是否存在直接边 u→v；越界编号返回 false。
func (g *Graph) HasEdge(u, v int) bool {
	if u < 0 || u >= g.n || v < 0 || v >= g.n {
		return false
	}
	return g.adj[u][v]
}
