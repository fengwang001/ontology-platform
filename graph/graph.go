// Package graph 构建并校验有向无环图：环检测、入度、Kahn 拓扑分层。
package graph

import (
	"fmt"
	"sort"
)

// Graph 是不可变的 DAG：边 from→to 表示 to 依赖 from。
type Graph struct {
	nodes  []string
	index  map[string]int
	deps   map[string][]string // to -> 它依赖的节点（入边来源）
	next   map[string][]string // from -> 依赖它的节点
	layers [][]string
	level  map[string]int
	topo   []string
}

// New 按 nodes 的声明顺序、deps（id -> 依赖的 id 列表）构建图。
// 构建时拒绝：重复节点、未知依赖、环（返回真实闭合路径）。
func New(nodes []string, deps map[string][]string) (*Graph, error) {
	g := &Graph{
		index: map[string]int{},
		deps:  map[string][]string{},
		next:  map[string][]string{},
		level: map[string]int{},
	}
	for i, n := range nodes {
		if _, dup := g.index[n]; dup {
			return nil, fmt.Errorf("graph: duplicate node %q", n)
		}
		g.index[n] = i
		g.nodes = append(g.nodes, n)
	}
	for to, ds := range deps {
		if _, ok := g.index[to]; !ok {
			return nil, fmt.Errorf("graph: unknown node %q in deps", to)
		}
		for _, from := range ds {
			if _, ok := g.index[from]; !ok {
				return nil, fmt.Errorf("graph: unknown dependency %q of %q", from, to)
			}
			g.deps[to] = append(g.deps[to], from)
			g.next[from] = append(g.next[from], to)
		}
	}
	if cyc := g.cycle(); cyc != nil {
		return nil, &CycleError{Path: cyc}
	}
	for n := range g.next { // 去 map 遍历的不确定性，固定为声明顺序
		sort.Slice(g.next[n], func(i, j int) bool { return g.index[g.next[n][i]] < g.index[g.next[n][j]] })
	}
	g.layerize()
	return g, nil
}

// Nodes 按声明顺序返回全部节点。
func (g *Graph) Nodes() []string { return append([]string(nil), g.nodes...) }

// Layers 返回 Kahn 拓扑分层：第 0 层无依赖，每层内依赖全部在前序层。
func (g *Graph) Layers() [][]string {
	out := make([][]string, len(g.layers))
	for i, l := range g.layers {
		out[i] = append([]string(nil), l...)
	}
	return out
}

// Level 返回节点层号；未知节点返回 -1。
func (g *Graph) Level(id string) int {
	if l, ok := g.level[id]; ok {
		return l
	}
	return -1
}

// Deps 返回某节点直接依赖的节点（声明顺序）。
func (g *Graph) Deps(id string) []string { return append([]string(nil), g.deps[id]...) }

// ReverseOrder 返回逆拓扑序（层号降序，同层按拓扑序降序），用于补偿。
func (g *Graph) ReverseOrder() []string {
	out := append([]string(nil), g.topo...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Width 返回最大层宽度。
func (g *Graph) Width() int {
	w := 0
	for _, l := range g.layers {
		if len(l) > w {
			w = len(l)
		}
	}
	return w
}

// CycleError 携带真实存在于输入中的一条闭合环路径。
type CycleError struct{ Path []string }

func (e *CycleError) Error() string {
	return fmt.Sprintf("graph: cycle detected: %v", e.Path)
}

func (g *Graph) layerize() {
	indeg := make([]int, len(g.nodes))
	for _, n := range g.nodes {
		indeg[g.index[n]] = len(g.deps[n])
	}
	var ready []int
	for _, n := range g.nodes { // 声明顺序入队，保证确定性
		if indeg[g.index[n]] == 0 {
			ready = append(ready, g.index[n])
		}
	}
	for len(ready) > 0 {
		var layer, nxt []int
		layer, ready = ready, nil
		for _, i := range layer {
			n := g.nodes[i]
			g.topo = append(g.topo, n)
			g.level[n] = len(g.layers)
			for _, m := range g.next[n] {
				j := g.index[m]
				indeg[j]--
				if indeg[j] == 0 {
					nxt = append(nxt, j)
				}
			}
		}
		names := make([]string, len(layer))
		for k, i := range layer {
			names[k] = g.nodes[i]
		}
		g.layers = append(g.layers, names)
		ready = nxt
	}
}

// cycle 用带路径栈的 DFS 找出一条真实闭合环；无环返回 nil。
func (g *Graph) cycle() []string {
	const white, gray, black = 0, 1, 2
	color := make([]int, len(g.nodes))
	stack := []string{}
	var dfs func(string) []string
	dfs = func(n string) []string {
		color[g.index[n]] = gray
		stack = append(stack, n)
		for _, m := range g.deps[n] { // 沿依赖边走
			switch color[g.index[m]] {
			case white:
				if p := dfs(m); p != nil {
					return p
				}
			case gray:
				for i, x := range stack { // 从 m 在栈中位置到栈顶即环
					if x == m {
						return append(append([]string(nil), stack[i:]...), m)
					}
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[g.index[n]] = black
		return nil
	}
	for _, n := range g.nodes {
		if color[g.index[n]] == white {
			if p := dfs(n); p != nil {
				return p
			}
		}
	}
	return nil
}
