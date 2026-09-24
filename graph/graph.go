// Package graph builds a DAG of tasks, detects cycles and computes
// deterministic topological layers.
package graph

import (
	"errors"
	"sort"
)

// ErrNodeNotFound is returned when an edge references an unknown task.
var ErrNodeNotFound = errors.New("graph: edge references unknown task")

// Graph is a directed graph of task IDs.
type Graph struct {
	nodes map[string]struct{}
	out   map[string]map[string]struct{}
	in    map[string]map[string]struct{}
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{
		nodes: map[string]struct{}{},
		out:   map[string]map[string]struct{}{},
		in:    map[string]map[string]struct{}{},
	}
}

// AddNode registers a task id; repeat adds are no-ops.
func (g *Graph) AddNode(id string) {
	if _, ok := g.nodes[id]; ok {
		return
	}
	g.nodes[id] = struct{}{}
	g.out[id] = map[string]struct{}{}
	g.in[id] = map[string]struct{}{}
}

// AddEdge records dependency "from must complete before to".
// Duplicate edges are idempotent.
func (g *Graph) AddEdge(from, to string) error {
	if _, ok := g.nodes[from]; !ok {
		return ErrNodeNotFound
	}
	if _, ok := g.nodes[to]; !ok {
		return ErrNodeNotFound
	}
	g.out[from][to] = struct{}{}
	g.in[to][from] = struct{}{}
	return nil
}

// Nodes returns all task ids sorted ascending.
func (g *Graph) Nodes() []string {
	ids := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Downstream returns the sorted successors of id.
func (g *Graph) Downstream(id string) []string {
	ids := make([]string, 0, len(g.out[id]))
	for s := range g.out[id] {
		ids = append(ids, s)
	}
	sort.Strings(ids)
	return ids
}

// indegree returns the number of unique incoming edges.
func (g *Graph) indegree(id string) int { return len(g.in[id]) }

// FindCycle returns one closed node path (edges consecutive, last==first
// implied by closing edge), or nil when the graph is acyclic.
func (g *Graph) FindCycle() []string {
	color := map[string]int{}
	var stack []string
	var dfs func(string) []string
	dfs = func(u string) []string {
		color[u] = 1
		stack = append(stack, u)
		next := g.Downstream(u)
		for _, v := range next {
			if color[v] == 1 {
				for i, n := range stack {
					if n == v {
						p := append([]string{}, stack[i:]...)
						return append(p, v)
					}
				}
			}
			if color[v] == 0 {
				if p := dfs(v); p != nil {
					return p
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[u] = 2
		return nil
	}
	for _, id := range g.Nodes() {
		if color[id] == 0 {
			if p := dfs(id); p != nil {
				return p
			}
		}
	}
	return nil
}

// Layers returns Kahn topological layers; each layer is id-sorted.
func (g *Graph) Layers() ([][]string, error) {
	indeg := map[string]int{}
	for id := range g.nodes {
		indeg[id] = g.indegree(id)
	}
	var layers [][]string
	seen := 0
	for seen < len(g.nodes) {
		var layer []string
		for _, id := range g.Nodes() {
			if indeg[id] == 0 {
				layer = append(layer, id)
			}
		}
		if len(layer) == 0 {
			return nil, errors.New("graph: cannot layer a cyclic graph")
		}
		for _, id := range layer {
			indeg[id] = -1
			seen++
			for _, s := range g.Downstream(id) {
				indeg[s]--
			}
		}
		layers = append(layers, layer)
	}
	return layers, nil
}
