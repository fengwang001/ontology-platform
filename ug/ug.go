// Package ug 提供无向简单图结构：邻接、加边与非法边校验。不依赖其他包。
package ug

import (
	"errors"
	"sort"
)

// 四类可判定哨兵错误，互不相同。
var (
	ErrNonPositiveN   = errors.New("ug: node count must be positive")
	ErrNodeOutOfRange = errors.New("ug: node out of range [0,n)")
	ErrSelfLoop       = errors.New("ug: self loop not allowed")
	ErrDuplicateEdge  = errors.New("ug: duplicate edge")
)

// Graph 是节点编号 [0,n) 的无向简单图。
type Graph struct {
	n     int
	adj   []map[int]struct{}
	edges int
}

// New 建图；n 非正时整体失败，返回 ErrNonPositiveN。
func New(n int) (*Graph, error) {
	if n <= 0 {
		return nil, ErrNonPositiveN
	}
	g := &Graph{n: n, adj: make([]map[int]struct{}, n)}
	for i := range g.adj {
		g.adj[i] = make(map[int]struct{})
	}
	return g, nil
}

// N 返回节点数。
func (g *Graph) N() int { return g.n }

// EdgeCount 返回已登记的无向边数。
func (g *Graph) EdgeCount() int { return g.edges }

// AddEdge 登记无向边 (u,v)。所有校验先于任何写入：
// 越界 → ErrNodeOutOfRange，自环 → ErrSelfLoop，重复 → ErrDuplicateEdge；
// 被拒时不改变图状态。
func (g *Graph) AddEdge(u, v int) error {
	if u < 0 || u >= g.n || v < 0 || v >= g.n {
		return ErrNodeOutOfRange
	}
	if u == v {
		return ErrSelfLoop
	}
	if g.HasEdge(u, v) {
		return ErrDuplicateEdge
	}
	g.adj[u][v] = struct{}{}
	g.adj[v][u] = struct{}{}
	g.edges++
	return nil
}

// HasEdge 报告 (u,v) 是否已登记。u、v 须在 [0,n) 内。
func (g *Graph) HasEdge(u, v int) bool {
	_, ok := g.adj[u][v]
	return ok
}

// Neighbors 按升序返回 u 的邻居。u 须在 [0,n) 内。
func (g *Graph) Neighbors(u int) []int {
	out := make([]int, 0, len(g.adj[u]))
	for v := range g.adj[u] {
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}
