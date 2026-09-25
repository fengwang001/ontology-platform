// Package graph 提供有向对象图：节点为对象，有向边为 Link。
package graph

import "fmt"

// Graph 是有向图。边按插入顺序保存，保证遍历序确定。
type Graph struct {
	nodes []string
	index map[string]bool
	out   map[string][]string
	in    map[string][]string
	edges map[[2]string]bool

	edgeExams int // HasCycle 单次调用的边考察次数（非导出，用于复杂度断言）
}

// New 返回空图。
func New() *Graph {
	return &Graph{
		index: make(map[string]bool),
		out:   make(map[string][]string),
		in:    make(map[string][]string),
		edges: make(map[[2]string]bool),
	}
}

// AddNode 加入节点；重名或空名报错。
func (g *Graph) AddNode(name string) error {
	if name == "" {
		return fmt.Errorf("graph: empty node name")
	}
	if g.index[name] {
		return fmt.Errorf("graph: node %q already exists", name)
	}
	g.index[name] = true
	g.nodes = append(g.nodes, name)
	return nil
}

// AddEdge 加入有向边 from->to；节点不存在、自环、重边均报错。
func (g *Graph) AddEdge(from, to string) error {
	if !g.index[from] {
		return fmt.Errorf("graph: node %q does not exist", from)
	}
	if !g.index[to] {
		return fmt.Errorf("graph: node %q does not exist", to)
	}
	if from == to {
		return fmt.Errorf("graph: self-loop on %q is not allowed", from)
	}
	if g.edges[[2]string{from, to}] {
		return fmt.Errorf("graph: duplicate edge %q -> %q", from, to)
	}
	g.edges[[2]string{from, to}] = true
	g.out[from] = append(g.out[from], to)
	g.in[to] = append(g.in[to], from)
	return nil
}

// Has 报告节点是否存在。
func (g *Graph) Has(name string) bool { return g.index[name] }

// Nodes 按插入顺序返回全部节点。
func (g *Graph) Nodes() []string { return append([]string(nil), g.nodes...) }

// Out 返回 name 的出边邻居（按插入顺序）。
func (g *Graph) Out(name string) []string { return g.out[name] }

// In 返回 name 的入边邻居（按插入顺序）。
func (g *Graph) In(name string) []string { return g.in[name] }

// 三色标记。
const (
	white = iota // 未访问
	gray         // 在递归栈上
	black        // 已完成
)

// HasCycle 用 DFS 三色标记判环：只有指向灰节点的边才构成环，
// 指向黑节点的边（如菱形依赖中的汇合边）不是环。O(V+E)。
func (g *Graph) HasCycle() bool {
	g.edgeExams = 0
	color := make(map[string]int, len(g.nodes))
	var visit func(n string) bool
	visit = func(n string) bool {
		color[n] = gray
		for _, next := range g.out[n] {
			g.edgeExams++
			switch color[next] {
			case gray:
				return true
			case white:
				if visit(next) {
					return true
				}
			}
		}
		color[n] = black
		return false
	}
	for _, n := range g.nodes {
		if color[n] == white && visit(n) {
			return true
		}
	}
	return false
}
