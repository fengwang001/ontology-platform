// Package graph 提供有向对象图：节点为对象，有向边为 Link。
// 环检测使用 DFS 三色标记（白/灰/黑），见 DESIGN.md 推导一。
package graph

import "errors"

var (
	ErrNodeExists   = errors.New("graph: node already exists")
	ErrNodeNotFound = errors.New("graph: node not found")
	ErrSelfLoop     = errors.New("graph: self loop not allowed")
	ErrEdgeExists   = errors.New("graph: edge already exists")
)

// Graph 是有向图，同时维护出边与入边邻接表。
type Graph struct {
	out map[string]map[string]struct{}
	in  map[string]map[string]struct{}

	// 非导出计数器：证明 HasCycle 的 O(V+E) 不变量。
	examined int
}

func New() *Graph {
	return &Graph{
		out: make(map[string]map[string]struct{}),
		in:  make(map[string]map[string]struct{}),
	}
}

func (g *Graph) AddNode(name string) error {
	if _, ok := g.out[name]; ok {
		return ErrNodeExists
	}
	g.out[name] = make(map[string]struct{})
	g.in[name] = make(map[string]struct{})
	return nil
}

func (g *Graph) AddEdge(from, to string) error {
	if from == to {
		return ErrSelfLoop
	}
	if _, ok := g.out[from]; !ok {
		return ErrNodeNotFound
	}
	if _, ok := g.out[to]; !ok {
		return ErrNodeNotFound
	}
	if _, ok := g.out[from][to]; ok {
		return ErrEdgeExists
	}
	g.out[from][to] = struct{}{}
	g.in[to][from] = struct{}{}
	return nil
}

func (g *Graph) Has(name string) bool {
	_, ok := g.out[name]
	return ok
}

// Nodes 返回全部节点名（顺序不定）。
func (g *Graph) Nodes() []string {
	names := make([]string, 0, len(g.out))
	for name := range g.out {
		names = append(names, name)
	}
	return names
}

// Out 返回 from 的出边邻居（顺序不定）。
func (g *Graph) Out(from string) []string {
	return keys(g.out[from])
}

// In 返回 to 的入边邻居（顺序不定）。
func (g *Graph) In(to string) []string {
	return keys(g.in[to])
}

func (g *Graph) Size() (nodes, edges int) {
	nodes = len(g.out)
	for _, adj := range g.out {
		edges += len(adj)
	}
	return nodes, edges
}

func keys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

// 三色标记。
const (
	white = iota // 未访问
	gray         // 在当前 DFS 栈上
	black        // 已彻底完成
)

// HasCycle 用 DFS 三色标记判环：只有指向灰节点的边才是回边（环）；
// 指向黑节点不是环（菱形汇合）。覆盖非连通图的所有分量。
func (g *Graph) HasCycle() bool {
	g.examined = 0
	color := make(map[string]int, len(g.out))
	for name := range g.out {
		if color[name] == white && g.dfs(name, color) {
			return true
		}
	}
	return false
}

func (g *Graph) dfs(node string, color map[string]int) bool {
	g.examined++ // 节点入栈一次
	color[node] = gray
	for next := range g.out[node] {
		g.examined++ // 每条出边恰考察一次
		switch color[next] {
		case gray:
			return true // 回边 → 环
		case white:
			if g.dfs(next, color) {
				return true
			}
		}
		// black：已完结区域，非环（菱形情形）
	}
	color[node] = black
	return false
}

// Examined 返回最近一次 HasCycle 的「节点访问 + 边考察」总次数，
// 用于测试断言其 ≤ V+E（O(V+E) 不变量）。
func (g *Graph) Examined() int { return g.examined }
