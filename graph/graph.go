// Package graph 负责任务图构建、环检测与拓扑分层，全部算法迭代实现。
package graph

import (
	"errors"
	"fmt"
	"sort"
)

// ErrUnknownNode 表示边引用了未加入图的任务。
var ErrUnknownNode = errors.New("graph: unknown node")

// Graph 是用集合存储邻接关系的有向图，重复加边自动幂等。
type Graph struct {
	succ map[string]map[string]struct{}
	pred map[string]map[string]struct{}
}

// New 创建空图。
func New() *Graph {
	return &Graph{
		succ: map[string]map[string]struct{}{},
		pred: map[string]map[string]struct{}{},
	}
}

// AddNode 加入一个任务节点，重复加入不报错。
func (g *Graph) AddNode(id string) {
	if _, ok := g.succ[id]; !ok {
		g.succ[id] = map[string]struct{}{}
		g.pred[id] = map[string]struct{}{}
	}
}

// AddEdge 加入 from -> to；若任一节点不存在则返回可 errors.Is 判定的错误。
func (g *Graph) AddEdge(from, to string) error {
	if _, ok := g.succ[from]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownNode, from)
	}
	if _, ok := g.succ[to]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownNode, to)
	}
	g.succ[from][to] = struct{}{}
	g.pred[to][from] = struct{}{}
	return nil
}

// Nodes 按字典序返回全部节点 ID。
func (g *Graph) Nodes() []string {
	ids := make([]string, 0, len(g.succ))
	for id := range g.succ {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Has 判断节点是否存在。
func (g *Graph) Has(id string) bool {
	_, ok := g.succ[id]
	return ok
}

// Edges 返回有向边数量。
func (g *Graph) Edges() int {
	n := 0
	for _, next := range g.succ {
		n += len(next)
	}
	return n
}

func sorted(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Successors 按字典序返回后继。
func (g *Graph) Successors(id string) []string { return sorted(g.succ[id]) }

// Predecessors 按字典序返回前驱。
func (g *Graph) Predecessors(id string) []string { return sorted(g.pred[id]) }

// Layers 用 Kahn 算法返回拓扑分层（层内按 ID 排序）；存在环时 layers 为 nil，
// cycle 是首尾闭合的真实环路径（每个相邻对都是输入中的边）。
func (g *Graph) Layers() (layers [][]string, cycle []string) {
	indeg := make(map[string]int, len(g.succ))
	var cur []string
	for id := range g.succ {
		indeg[id] = len(g.pred[id])
		if indeg[id] == 0 {
			cur = append(cur, id)
		}
	}
	sort.Strings(cur)
	done := 0
	for len(cur) > 0 {
		layers = append(layers, cur)
		var next []string
		for _, id := range cur {
			done++
			for _, s := range g.Successors(id) {
				indeg[s]--
				if indeg[s] == 0 {
					next = append(next, s)
				}
			}
		}
		sort.Strings(next)
		cur = next
	}
	if done != len(g.succ) {
		return nil, g.findCycle(indeg)
	}
	return layers, nil
}

// findCycle 在 Kahn 未消完的子图（indeg>0）上做迭代三色 DFS，遇到灰点即还原环。
func (g *Graph) findCycle(indeg map[string]int) []string {
	const white, gray, black = 0, 1, 2
	color := map[string]int{}
	parent := map[string]string{}
	type frame struct {
		node string
		next int
	}
	for _, start := range g.Nodes() {
		if indeg[start] == 0 || color[start] != white {
			continue
		}
		stack := []frame{{node: start}}
		color[start] = gray
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			next := g.Successors(top.node)
			if top.next >= len(next) {
				color[top.node] = black
				stack = stack[:len(stack)-1]
				continue
			}
			s := next[top.next]
			top.next++
			if indeg[s] == 0 {
				continue
			}
			switch color[s] {
			case white:
				color[s] = gray
				parent[s] = top.node
				stack = append(stack, frame{node: s})
			case gray:
				var path []string
				for n := top.node; ; n = parent[n] {
					path = append(path, n)
					if n == s {
						break
					}
				}
				for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
					path[i], path[j] = path[j], path[i]
				}
				return append(path, s)
			}
		}
	}
	return nil
}
