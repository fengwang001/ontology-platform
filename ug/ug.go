// Package ug 提供无向简单图结构：邻接、加边与非法边校验。不依赖其他包。
package ug

import "errors"

// 可判定的哨兵错误，三者互不相同。
var (
	ErrOutOfRange = errors.New("ug: 边引用了 [0,n) 之外的节点")
	ErrSelfLoop   = errors.New("ug: 不允许自环 (u==v)")
	ErrDuplicate  = errors.New("ug: 重复登记同一条无向边")
)

// Graph 是节点编号 [0,n) 上的无向简单图，邻接表用 map 去重。
type Graph struct {
	n   int
	adj []map[int]struct{}
	m   int
}

// New 建 n 个节点的空图。n 必须非负（n 的合法性由上层 api 把关）。
func New(n int) *Graph {
	if n < 0 {
		panic("ug: negative node count")
	}
	return &Graph{n: n, adj: make([]map[int]struct{}, n)}
}

// N 返回节点数。
func (g *Graph) N() int { return g.n }

// EdgeCount 返回已登记的无向边数。
func (g *Graph) EdgeCount() int { return g.m }

// AddEdge 登记无向边 (u,v)。任何非法边都整体失败、不改变图状态。
func (g *Graph) AddEdge(u, v int) error {
	if u < 0 || u >= g.n || v < 0 || v >= g.n {
		return ErrOutOfRange
	}
	if u == v {
		return ErrSelfLoop
	}
	if g.HasEdge(u, v) {
		return ErrDuplicate
	}
	if g.adj[u] == nil {
		g.adj[u] = make(map[int]struct{})
	}
	if g.adj[v] == nil {
		g.adj[v] = make(map[int]struct{})
	}
	g.adj[u][v] = struct{}{}
	g.adj[v][u] = struct{}{}
	g.m++
	return nil
}

// HasEdge 报告无向边 (u,v) 是否已登记。
func (g *Graph) HasEdge(u, v int) bool {
	if u < 0 || u >= g.n || v < 0 || v >= g.n {
		return false
	}
	_, ok := g.adj[u][v]
	return ok
}

// Neighbors 返回 v 的邻居编号切片（顺序未定）。
func (g *Graph) Neighbors(v int) []int {
	out := make([]int, 0, len(g.adj[v]))
	for w := range g.adj[v] {
		out = append(out, w)
	}
	return out
}
