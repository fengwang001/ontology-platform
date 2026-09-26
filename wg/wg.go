// Package wg 提供加权无向图结构：邻接、加边与非法边校验、边权维护。
// 不依赖其他包。本包不做并发控制，并发安全由上层（api）保证。
package wg

import "errors"

var (
	ErrTooFewNodes       = errors.New("wg: node count < 2")
	ErrNodeOutOfRange    = errors.New("wg: node out of range [0,n)")
	ErrSelfLoop          = errors.New("wg: self loop")
	ErrDuplicateEdge     = errors.New("wg: duplicate edge")
	ErrNonPositiveWeight = errors.New("wg: weight must be positive")
)

// Graph 是节点 0..n-1 上的加权无向图，同一对节点至多一条边。
type Graph struct {
	n     int
	adj   []map[int]int64
	edges int
}

// New 创建 n 个节点的空图；n < 2 时整体失败。
func New(n int) (*Graph, error) {
	if n < 2 {
		return nil, ErrTooFewNodes
	}
	return &Graph{n: n, adj: make([]map[int]int64, n)}, nil
}

// AddEdge 登记无向边 (u,v,w)。任何非法入参都整体失败、不改图状态。
func (g *Graph) AddEdge(u, v int, w int64) error {
	if u < 0 || u >= g.n || v < 0 || v >= g.n {
		return ErrNodeOutOfRange
	}
	if u == v {
		return ErrSelfLoop
	}
	if w <= 0 {
		return ErrNonPositiveWeight
	}
	if _, ok := g.adj[u][v]; ok {
		return ErrDuplicateEdge
	}
	if g.adj[u] == nil {
		g.adj[u] = map[int]int64{}
	}
	if g.adj[v] == nil {
		g.adj[v] = map[int]int64{}
	}
	g.adj[u][v], g.adj[v][u] = w, w
	g.edges++
	return nil
}

// N 返回节点数。
func (g *Graph) N() int { return g.n }

// EdgeCount 返回已登记边数。
func (g *Graph) EdgeCount() int { return g.edges }

// Weight 返回边 (u,v) 的权，无边返回 0。
func (g *Graph) Weight(u, v int) int64 { return g.adj[u][v] }

// ForEachEdge 对每条无向边恰好调用一次 f（u < v）。
func (g *Graph) ForEachEdge(f func(u, v int, w int64)) {
	for u := 0; u < g.n; u++ {
		for v, w := range g.adj[u] {
			if u < v {
				f(u, v, w)
			}
		}
	}
}
