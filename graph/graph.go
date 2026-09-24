// Package graph 构建任务有向图，提供环检测与拓扑分层。
package graph

import (
	"sort"

	"ontology/fail"
)

// Graph 中边 from→to 表示 from 必须先于 to 完成。
type Graph struct {
	nodes map[string]struct{}
	preds map[string]map[string]struct{}
	succs map[string]map[string]struct{}
	edges int
}

func New() *Graph {
	return &Graph{
		nodes: map[string]struct{}{},
		preds: map[string]map[string]struct{}{},
		succs: map[string]map[string]struct{}{},
	}
}

// AddTask 注册一个任务，重复注册幂等。
func (g *Graph) AddTask(id string) {
	g.nodes[id] = struct{}{}
}

// AddEdge 添加依赖边 from→to；两端任务必须先注册，重复加边幂等。
func (g *Graph) AddEdge(from, to string) error {
	if _, ok := g.nodes[from]; !ok {
		return fail.ErrUnknown
	}
	if _, ok := g.nodes[to]; !ok {
		return fail.ErrUnknown
	}
	if g.succs[from] == nil {
		g.succs[from] = map[string]struct{}{}
	}
	if _, dup := g.succs[from][to]; dup {
		return nil
	}
	g.succs[from][to] = struct{}{}
	if g.preds[to] == nil {
		g.preds[to] = map[string]struct{}{}
	}
	g.preds[to][from] = struct{}{}
	g.edges++
	return nil
}

// HasEdge 报告 from→to 边是否存在。
func (g *Graph) HasEdge(from, to string) bool {
	_, ok := g.succs[from][to]
	return ok
}

// Nodes 按字典序返回全部任务 ID。
func (g *Graph) Nodes() []string {
	out := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Edges 返回去重后的边数。
func (g *Graph) Edges() int { return g.edges }

// Succs 按字典序返回 id 的直接后继。
func (g *Graph) Succs(id string) []string { return sorted(g.succs[id]) }

// Preds 按字典序返回 id 的直接前驱。
func (g *Graph) Preds(id string) []string { return sorted(g.preds[id]) }

func sorted(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Cycle 用迭代三色 DFS 检测环；无环返回 nil，
// 有环返回一条真实环路径 [v0, v1, ..., v0]（每条边都在图中、首尾闭合）。
func (g *Graph) Cycle() []string {
	const (
		white = iota
		gray
		black
	)
	color := map[string]int{}
	type frame struct {
		node  string
		succs []string
		next  int
	}
	for _, start := range g.Nodes() {
		if color[start] != white {
			continue
		}
		color[start] = gray
		stack := []frame{{node: start, succs: g.Succs(start)}}
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			if top.next >= len(top.succs) {
				color[top.node] = black
				stack = stack[:len(stack)-1]
				continue
			}
			n := top.succs[top.next]
			top.next++
			switch color[n] {
			case white:
				color[n] = gray
				stack = append(stack, frame{node: n, succs: g.Succs(n)})
			case gray:
				path := []string{n}
				for i := len(stack) - 1; i >= 0; i-- {
					path = append([]string{stack[i].node}, path...)
					if stack[i].node == n {
						break
					}
				}
				return path
			}
		}
	}
	return nil
}

// Layers 用 Kahn 算法做拓扑分层，层内按 ID 字典序；有环返回 nil。
func (g *Graph) Layers() [][]string {
	indeg := map[string]int{}
	for _, id := range g.Nodes() {
		indeg[id] = len(g.preds[id])
	}
	var layers [][]string
	done := 0
	for {
		var layer []string
		for _, id := range g.Nodes() {
			if indeg[id] == 0 {
				layer = append(layer, id)
			}
		}
		if len(layer) == 0 {
			break
		}
		for _, id := range layer {
			indeg[id] = -1
			for _, s := range g.Succs(id) {
				indeg[s]--
			}
		}
		done += len(layer)
		layers = append(layers, layer)
	}
	if done != len(g.nodes) {
		return nil
	}
	return layers
}
