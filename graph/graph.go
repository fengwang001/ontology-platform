// Package graph 构建有向无环任务图，做环检测与拓扑分层。
package graph

import (
	"errors"
	"sort"
)

// ErrUnknownTask 表示边引用了尚未注册的任务。
var ErrUnknownTask = errors.New("graph: unknown task")

// Graph 是以邻接表存储的有向图。边 u->v 表示 v 依赖 u。
type Graph struct {
	nodes map[string]bool
	succ  map[string]map[string]bool
	pred  map[string]map[string]bool
}

// New 创建空图。
func New() *Graph {
	return &Graph{
		nodes: map[string]bool{},
		succ:  map[string]map[string]bool{},
		pred:  map[string]map[string]bool{},
	}
}

// AddTask 注册一个任务，重复注册幂等。
func (g *Graph) AddTask(id string) {
	if g.nodes[id] {
		return
	}
	g.nodes[id] = true
	g.succ[id] = map[string]bool{}
	g.pred[id] = map[string]bool{}
}

// AddEdge 增加依赖边 from->to（to 依赖 from）。重复边幂等。
func (g *Graph) AddEdge(from, to string) error {
	if !g.nodes[from] || !g.nodes[to] {
		return ErrUnknownTask
	}
	g.succ[from][to] = true
	g.pred[to][from] = true
	return nil
}

// Tasks 返回按 ID 升序排列的全部任务。
func (g *Graph) Tasks() []string {
	ids := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Successors 返回 v 的直接后继（升序）。
func (g *Graph) Successors(v string) []string {
	out := make([]string, 0, len(g.succ[v]))
	for id := range g.succ[v] {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Predecessors 返回 v 的直接前驱（升序）。
func (g *Graph) Predecessors(v string) []string {
	in := make([]string, 0, len(g.pred[v]))
	for id := range g.pred[v] {
		in = append(in, id)
	}
	sort.Strings(in)
	return in
}

// N 返回节点数，E 返回去重后的边数。
func (g *Graph) N() int { return len(g.nodes) }

// E 返回边数。
func (g *Graph) E() int {
	n := 0
	for _, m := range g.succ {
		n += len(m)
	}
	return n
}

// HasEdge 报告是否存在边 from->to。
func (g *Graph) HasEdge(from, to string) bool {
	return g.succ[from][to]
}

// Cycle 返回一条真实环路径（首尾闭合），无环时返回 nil。
// 使用迭代三色 DFS 显式栈，深链不会导致栈溢出。
func (g *Graph) Cycle() []string {
	const white, gray, black = 0, 1, 2
	color := map[string]int{}
	type frame struct {
		u    string
		next int
	}
	for _, start := range g.Tasks() {
		if color[start] != white {
			continue
		}
		color[start] = gray
		stack := []frame{{u: start, next: 0}}
		path := []string{start}
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			next := g.Successors(top.u)
			if top.next >= len(next) {
				color[top.u] = black
				stack = stack[:len(stack)-1]
				path = path[:len(path)-1]
				continue
			}
			v := next[top.next]
			top.next++
			if color[v] == gray {
				i := 0
				for path[i] != v {
					i++
				}
				cyc := append([]string{}, path[i:]...)
				return append(cyc, v)
			}
			if color[v] == white {
				color[v] = gray
				stack = append(stack, frame{u: v, next: 0})
				path = append(path, v)
			}
		}
	}
	return nil
}

// Layers 返回拓扑分层：同一层内任务互不依赖；层内按 ID 升序。无环时第二个
// 返回值为 true；有环时返回 nil,false。迭代 Kahn，深链不栈溢出。
func (g *Graph) Layers() ([][]string, bool) {
	if g.Cycle() != nil {
		return nil, false
	}
	indeg := map[string]int{}
	for id := range g.nodes {
		indeg[id] = len(g.pred[id])
	}
	var layer []string
	for id, d := range indeg {
		if d == 0 {
			layer = append(layer, id)
		}
	}
	sort.Strings(layer)
	var layers [][]string
	seen := 0
	for len(layer) > 0 {
		layers = append(layers, append([]string{}, layer...))
		var nextLayer []string
		for _, u := range layer {
			seen++
			for _, v := range g.Successors(u) {
				indeg[v]--
				if indeg[v] == 0 {
					nextLayer = append(nextLayer, v)
				}
			}
		}
		sort.Strings(nextLayer)
		layer = nextLayer
	}
	if seen != len(g.nodes) {
		return nil, false
	}
	return layers, true
}
