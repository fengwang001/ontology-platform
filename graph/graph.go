package graph

import (
	"errors"
	"fmt"
	"sort"
)

// ErrMissingNode 表示一条边引用了图中不存在的任务。
var ErrMissingNode = errors.New("graph: edge references missing node")

// Graph 是以邻接表表示的有向图：边 from -> to 表示 to 依赖 from。
type Graph struct {
	nodes map[string]struct{}
	succ  map[string]map[string]struct{}
	indeg map[string]int
}

func New() *Graph {
	return &Graph{
		nodes: map[string]struct{}{},
		succ:  map[string]map[string]struct{}{},
		indeg: map[string]int{},
	}
}

func (g *Graph) Add(id string) {
	if _, ok := g.nodes[id]; ok {
		return
	}
	g.nodes[id] = struct{}{}
	g.succ[id] = map[string]struct{}{}
	g.indeg[id] = 0
}

// AddEdge 加入依赖边 to 依赖 from；重复加同一条边是幂等的。
func (g *Graph) AddEdge(from, to string) error {
	if _, ok := g.nodes[from]; !ok {
		return fmt.Errorf("%w: %q", ErrMissingNode, from)
	}
	if _, ok := g.nodes[to]; !ok {
		return fmt.Errorf("%w: %q", ErrMissingNode, to)
	}
	if _, dup := g.succ[from][to]; dup {
		return nil
	}
	g.succ[from][to] = struct{}{}
	g.indeg[to]++
	return nil
}

func (g *Graph) Has(id string) bool {
	_, ok := g.nodes[id]
	return ok
}

func (g *Graph) Nodes() []string {
	ids := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (g *Graph) InDegree(id string) int { return g.indeg[id] }

func (g *Graph) HasEdge(from, to string) bool {
	_, ok := g.succ[from][to]
	return ok
}

// Successors 返回 from 的直接下游（ID 升序）。
func (g *Graph) Successors(from string) []string { return g.succSorted(from) }

func (g *Graph) succSorted(id string) []string {
	out := make([]string, 0, len(g.succ[id]))
	for s := range g.succ[id] {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Cycle 用迭代 DFS 找一条真实存在的环；返回首尾相同的节点序列。
func (g *Graph) Cycle() []string {
	const white, gray, black = 0, 1, 2
	color := map[string]int{}
	for _, start := range g.Nodes() {
		if color[start] != white {
			continue
		}
		type frame struct {
			id  string
			nxt int
		}
		stack := []frame{{id: start}}
		path := []string{}
		onPath := map[string]int{}
		for len(stack) > 0 {
			top := len(stack) - 1
			f := &stack[top]
			if f.nxt == 0 {
				color[f.id] = gray
				onPath[f.id] = len(path)
				path = append(path, f.id)
			}
			nexts := g.succSorted(f.id)
			if f.nxt < len(nexts) {
				child := nexts[f.nxt]
				f.nxt++
				switch color[child] {
				case white:
					stack = append(stack, frame{id: child})
				case gray:
					return append(path[onPath[child]:], child)
				}
				continue
			}
			color[f.id] = black
			delete(onPath, f.id)
			path = path[:len(path)-1]
			stack = stack[:top]
		}
	}
	return nil
}

// Layers 做 Kahn 拓扑分层：每层内的任务互不依赖、可并行。存在环时返回 nil。
func (g *Graph) Layers() [][]string {
	deg := make(map[string]int, len(g.nodes))
	for id := range g.nodes {
		deg[id] = g.indeg[id]
	}
	var layers [][]string
	placed := 0
	for placed < len(g.nodes) {
		var layer []string
		for _, id := range g.Nodes() {
			if deg[id] == 0 {
				layer = append(layer, id)
			}
		}
		if len(layer) == 0 {
			return nil
		}
		layers = append(layers, layer)
		for _, id := range layer {
			deg[id] = -1
			placed++
			for s := range g.succ[id] {
				deg[s]--
			}
		}
	}
	return layers
}
