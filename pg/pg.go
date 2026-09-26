// Package pg 提供平面图结构：邻接表、环序、加边与 SetRotation 校验。
// 不依赖其他包。
package pg

import "errors"

// 可判定的哨兵错误，互不相同。
var (
	ErrNonPositiveN   = errors.New("pg: 节点数必须为正")
	ErrNodeOutOfRange = errors.New("pg: 节点编号超出 [0,n)")
	ErrSelfLoop       = errors.New("pg: 不允许自环")
	ErrDuplicateEdge  = errors.New("pg: 重复边")
	ErrBadRotation    = errors.New("pg: 环序不是该节点邻居的一个排列")
)

// Graph 是无向简单图加组合嵌入（每节点一个环序）。
type Graph struct {
	n     int
	adj   [][]int       // 邻居，按加边顺序
	rot   [][]int       // 环序；nil 表示未显式设置
	pos   []map[int]int // pos[v][u] = u 在 rot[v] 中的下标，O(1) 定位前驱
	edges map[[2]int]struct{}
	ecnt  int
}

// New 建 n 个节点的空图；n 非正整体失败。
func New(n int) (*Graph, error) {
	if n <= 0 {
		return nil, ErrNonPositiveN
	}
	return &Graph{
		n:     n,
		adj:   make([][]int, n),
		rot:   make([][]int, n),
		pos:   make([]map[int]int, n),
		edges: make(map[[2]int]struct{}),
	}, nil
}

// N 返回节点数。
func (g *Graph) N() int { return g.n }

// EdgeCount 返回无向边数。
func (g *Graph) EdgeCount() int { return g.ecnt }

// AddEdge 加无向边 u-v。全部校验通过后才改状态；任一校验失败整体失败。
func (g *Graph) AddEdge(u, v int) error {
	if u < 0 || u >= g.n || v < 0 || v >= g.n {
		return ErrNodeOutOfRange
	}
	if u == v {
		return ErrSelfLoop
	}
	key := [2]int{min(u, v), max(u, v)}
	if _, dup := g.edges[key]; dup {
		return ErrDuplicateEdge
	}
	g.edges[key] = struct{}{}
	g.adj[u] = append(g.adj[u], v)
	g.adj[v] = append(g.adj[v], u)
	g.rot[u], g.pos[u] = nil, nil // 邻居集变了，旧环序作废
	g.rot[v], g.pos[v] = nil, nil
	g.ecnt++
	return nil
}

// SetRotation 把 order 设为 v 的环序；order 必须是 v 当前全部邻居的一个排列。
func (g *Graph) SetRotation(v int, order []int) error {
	if v < 0 || v >= g.n {
		return ErrNodeOutOfRange
	}
	if len(order) != len(g.adj[v]) {
		return ErrBadRotation
	}
	want := make(map[int]int, len(g.adj[v]))
	for _, u := range g.adj[v] {
		want[u]++
	}
	seen := make(map[int]struct{}, len(order))
	p := make(map[int]int, len(order))
	for i, u := range order {
		if want[u] == 0 {
			return ErrBadRotation // 含非邻居
		}
		if _, dup := seen[u]; dup {
			return ErrBadRotation // 含重复
		}
		seen[u] = struct{}{}
		p[u] = i
	}
	g.rot[v] = append([]int(nil), order...)
	g.pos[v] = p
	return nil
}

// Neighbors 返回 v 的邻居副本（加边顺序）。
func (g *Graph) Neighbors(v int) []int { return append([]int(nil), g.adj[v]...) }

// Rotation 返回 v 的环序；未显式设置时退化为加边顺序。
func (g *Graph) Rotation(v int) []int {
	if g.rot[v] != nil {
		return append([]int(nil), g.rot[v]...)
	}
	return g.Neighbors(v)
}

// Prev 返回 v 的环序中紧挨 u 前面的邻居（循环）。用下标索引 O(1) 定位，
// 只检查 1 个邻接槽位，不扫描整个环序。u 非邻居时 ok=false。
func (g *Graph) Prev(v, u int) (w int, ok bool) {
	rot := g.rot[v]
	var i int
	if rot != nil {
		j, hit := g.pos[v][u]
		if !hit {
			return 0, false
		}
		i = j
	} else { // 未显式设置：环序即加边顺序，线性定位（仅作兜底）
		i = -1
		for j, x := range g.adj[v] {
			if x == u {
				i = j
				break
			}
		}
		if i < 0 {
			return 0, false
		}
		rot = g.adj[v]
	}
	return rot[(i-1+len(rot))%len(rot)], true
}
