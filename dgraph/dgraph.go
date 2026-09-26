// Package dgraph 提供有向图结构：邻接表、加边校验、从入口 0 的可达性标记。
package dgraph

import "errors"

var (
	ErrNonPositive = errors.New("dgraph: node count must be positive")
	ErrNodeRange   = errors.New("dgraph: node index out of range")
	ErrSelfLoop    = errors.New("dgraph: self loop not allowed")
	ErrDuplicate   = errors.New("dgraph: duplicate edge")
)

// Graph 是节点编号 [0,n) 的有向图，入口固定为 0。
type Graph struct {
	n    int
	succ [][]int
	seen map[[2]int]struct{}
	edge int
}

// New 建图；n 非正时整体失败，不产出任何状态。
func New(n int) (*Graph, error) {
	if n <= 0 {
		return nil, ErrNonPositive
	}
	return &Graph{n: n, succ: make([][]int, n), seen: make(map[[2]int]struct{})}, nil
}

// AddEdge 登记有向边 u→v。任何校验失败都不改变图状态。
func (g *Graph) AddEdge(u, v int) error {
	if u < 0 || u >= g.n || v < 0 || v >= g.n {
		return ErrNodeRange
	}
	if u == v {
		return ErrSelfLoop
	}
	k := [2]int{u, v}
	if _, ok := g.seen[k]; ok {
		return ErrDuplicate
	}
	g.seen[k] = struct{}{}
	g.succ[u] = append(g.succ[u], v)
	g.edge++
	return nil
}

func (g *Graph) N() int           { return g.n }
func (g *Graph) Edges() int       { return g.edge }
func (g *Graph) Succ(v int) []int { return g.succ[v] }

// Reachable 返回从入口 0 可达的节点标记。
func (g *Graph) Reachable() []bool {
	vis := make([]bool, g.n)
	vis[0] = true
	stack := []int{0}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, w := range g.succ[v] {
			if !vis[w] {
				vis[w] = true
				stack = append(stack, w)
			}
		}
	}
	return vis
}

// Preds 返回每个节点的直接前驱列表（按加边顺序）。
func (g *Graph) Preds() [][]int {
	p := make([][]int, g.n)
	for u := 0; u < g.n; u++ {
		for _, v := range g.succ[u] {
			p[v] = append(p[v], u)
		}
	}
	return p
}
