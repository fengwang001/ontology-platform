// Package dag 实现有向图结构：节点入度、出边邻接表、加边校验与环检测。
// 不依赖本工程其他包。
package dag

import (
	"errors"
	"sync"
)

// 可判定哨兵错误，互不相同。
var (
	ErrSelfLoop       = errors.New("dag: self loop edge")
	ErrNodeOutOfRange = errors.New("dag: node out of range [0,n)")
	ErrDuplicateEdge  = errors.New("dag: duplicate edge")
)

// Graph 是节点编号 [0,n) 上的有向图，并发安全。
type Graph struct {
	mu    sync.RWMutex
	n     int
	indeg []int
	adj   [][]int
	edges map[[2]int]struct{}
}

// New 创建 n 个节点的空图。
func New(n int) *Graph {
	if n < 0 {
		panic("dag: negative node count")
	}
	return &Graph{
		n:     n,
		indeg: make([]int, n),
		adj:   make([][]int, n),
		edges: make(map[[2]int]struct{}),
	}
}

// AddEdge 登记有向边 u→v。只登记，不触发排序。
// 自环、越界节点、重复边均被拒绝且不改任何状态。
func (g *Graph) AddEdge(u, v int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if u == v {
		return ErrSelfLoop
	}
	if u < 0 || u >= g.n || v < 0 || v >= g.n {
		return ErrNodeOutOfRange
	}
	if _, ok := g.edges[[2]int{u, v}]; ok {
		return ErrDuplicateEdge
	}
	g.edges[[2]int{u, v}] = struct{}{}
	g.adj[u] = append(g.adj[u], v)
	g.indeg[v]++
	return nil
}

// EdgeCount 返回已登记边数。
func (g *Graph) EdgeCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.edges)
}

// Snapshot 返回当前入度与邻接表的只读副本，供排序使用。
// 拷贝后排序过程不触碰原图，保证并发读安全、失败不留痕。
func (g *Graph) Snapshot() (indeg []int, adj [][]int) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	indeg = make([]int, g.n)
	copy(indeg, g.indeg)
	adj = make([][]int, g.n)
	for i := range g.adj {
		adj[i] = make([]int, len(g.adj[i]))
		copy(adj[i], g.adj[i])
	}
	return indeg, adj
}

// N 返回节点数。
func (g *Graph) N() int { return g.n }
