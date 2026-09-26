// Package dg 是有向图结构：邻接矩阵、加边与非法边校验。不依赖其他包。
package dg

import "errors"

// 加边相关的可判定哨兵错误（n 非正的错误由上层 api 定义）。
var (
	ErrNodeOutOfRange = errors.New("dg: edge references a node outside [0,n)")
	ErrSelfLoop       = errors.New("dg: self loop u==v is not allowed")
	ErrDuplicateEdge  = errors.New("dg: edge (u,v) already exists")
)

// Graph 是节点编号固定为 [0,n) 的有向图，状态在进程内存。
type Graph struct {
	n         int
	adj       [][]bool
	edgeCount int
}

// New 创建 n 个节点的空图。调用方（api）须保证 n >= 1。
func New(n int) *Graph {
	adj := make([][]bool, n)
	for i := range adj {
		adj[i] = make([]bool, n)
	}
	return &Graph{n: n, adj: adj}
}

// N 返回节点数。
func (g *Graph) N() int { return g.n }

// HasEdge 报告是否存在直接边 u→v（仅在范围内查询时合法）。
func (g *Graph) HasEdge(u, v int) bool { return g.adj[u][v] }

// AddEdge 登记有向边 u→v。
// 节点越界、自环、重复边分别返回互不相同的哨兵错误；
// 所有校验先于状态写入，故任何拒绝都不留痕。
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
	g.edgeCount++
	return nil
}

// EdgeCount 返回已登记的边数。
func (g *Graph) EdgeCount() int { return g.edgeCount }
