// Package pg 定义无向简单图及其组合嵌入（每个节点一个环序 rotation）。
// 本包不依赖其他包；所有修改类操作先整体校验、后落盘，失败不留痕。
package pg

import (
	"errors"
	"sort"
	"sync"
)

// 可判定的哨兵错误，五者互不相同（自环与重复边分别给出）。
var (
	ErrBadN          = errors.New("pg: n must be positive")
	ErrBadVertex     = errors.New("pg: vertex index out of range")
	ErrSelfLoop      = errors.New("pg: self loops are not allowed")
	ErrDuplicateEdge = errors.New("pg: edge already exists")
	ErrBadRotation   = errors.New("pg: rotation must be a permutation of the vertex's neighbors")
)

// Snapshot 是某一时刻图与环序的不可变拷贝，供 face 包脱离锁做追踪。
type Snapshot struct {
	N        int
	Edges    [][2]int // 规范化（小端在前）、按字典序
	Rotation [][]int  // 每个节点一份防御性拷贝；未设置时为 nil
	HasRot   []bool
}

// Graph 是进程内的无向图 + 环序状态，方法可并发调用。
type Graph struct {
	mu      sync.RWMutex
	n       int
	edgeSet map[[2]int]struct{}
	adj     []map[int]struct{}
	rot     [][]int
	hasRot  []bool
}

// New 固定节点编号区间 [0, n)；n 非正返回 ErrBadN。
func New(n int) (*Graph, error) {
	if n <= 0 {
		return nil, ErrBadN
	}
	g := &Graph{
		n:       n,
		edgeSet: make(map[[2]int]struct{}),
		adj:     make([]map[int]struct{}, n),
		rot:     make([][]int, n),
		hasRot:  make([]bool, n),
	}
	for i := range g.adj {
		g.adj[i] = make(map[int]struct{})
	}
	return g, nil
}

// N 返回节点数。
func (g *Graph) N() int { return g.n }

// AddEdge 加无向简单边；越界 / 自环 / 重复分别返回对应哨兵错误，拒绝时状态不变。
func (g *Graph) AddEdge(u, v int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if u < 0 || u >= g.n || v < 0 || v >= g.n {
		return ErrBadVertex
	}
	if u == v {
		return ErrSelfLoop
	}
	a, b := u, v
	if a > b {
		a, b = b, a
	}
	key := [2]int{a, b}
	if _, ok := g.edgeSet[key]; ok {
		return ErrDuplicateEdge
	}
	// 全部校验通过后才修改：两端邻接集 + 边集合，一次成型。
	g.edgeSet[key] = struct{}{}
	g.adj[u][v] = struct{}{}
	g.adj[v][u] = struct{}{}
	return nil
}

// SetRotation 指定节点 v 的环序：order 必须恰好是 v 现有邻居的一个排列。
func (g *Graph) SetRotation(v int, order []int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if v < 0 || v >= g.n {
		return ErrBadVertex
	}
	if len(order) != len(g.adj[v]) {
		return ErrBadRotation
	}
	seen := make(map[int]struct{}, len(order))
	got := make([]int, len(order))
	for i, w := range order {
		if w < 0 || w >= g.n {
			return ErrBadRotation
		}
		if _, dup := seen[w]; dup {
			return ErrBadRotation
		}
		if _, ok := g.adj[v][w]; !ok {
			return ErrBadRotation // 含非邻居（漏邻居由长度判定）
		}
		seen[w] = struct{}{}
		got[i] = w
	}
	// 校验全过才落盘（防御性拷贝，避免调用方事后改切片）。
	g.rot[v] = got
	g.hasRot[v] = true
	return nil
}

// Snapshot 在读锁下取出整张图与环序的拷贝。
func (g *Graph) Snapshot() Snapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()
	s := Snapshot{N: g.n, Rotation: make([][]int, g.n), HasRot: make([]bool, g.n)}
	for e := range g.edgeSet {
		s.Edges = append(s.Edges, e)
	}
	sort.Slice(s.Edges, func(i, j int) bool {
		if s.Edges[i][0] != s.Edges[j][0] {
			return s.Edges[i][0] < s.Edges[j][0]
		}
		return s.Edges[i][1] < s.Edges[j][1]
	})
	for v := 0; v < g.n; v++ {
		s.HasRot[v] = g.hasRot[v]
		if g.hasRot[v] {
			s.Rotation[v] = append([]int(nil), g.rot[v]...)
		}
	}
	return s
}
