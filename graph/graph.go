// Package graph builds and validates a directed acyclic graph and computes
// topological layers. It depends on no other package in this module.
package graph

import (
	"errors"
	"fmt"
)

// ErrCycle is returned when the input graph contains a directed cycle.
var ErrCycle = errors.New("graph: cycle detected")

// Graph is an adjacency-list directed graph.
type Graph struct {
	order []string
	nodes map[string]struct{}
	edges map[string][]string
	indeg map[string]int
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{
		nodes: map[string]struct{}{},
		edges: map[string][]string{},
		indeg: map[string]int{},
	}
}

// AddNode registers a node. Duplicate names are ignored.
func (g *Graph) AddNode(id string) {
	if _, ok := g.nodes[id]; ok {
		return
	}
	g.nodes[id] = struct{}{}
	g.order = append(g.order, id)
	g.indeg[id] = 0
}

// AddEdge records a dependency: from must complete before to may run.
func (g *Graph) AddEdge(from, to string) error {
	if _, ok := g.nodes[from]; !ok {
		return fmt.Errorf("graph: unknown node %q", from)
	}
	if _, ok := g.nodes[to]; !ok {
		return fmt.Errorf("graph: unknown node %q", to)
	}
	g.edges[from] = append(g.edges[from], to)
	g.indeg[to]++
	return nil
}

// Nodes returns node ids in insertion order.
func (g *Graph) Nodes() []string { return append([]string(nil), g.order...) }

// InDegree returns the in-degree of a node.
func (g *Graph) InDegree(id string) int { return g.indeg[id] }

// Edges returns the outgoing edges of a node in insertion order.
func (g *Graph) Edges(from string) []string { return append([]string(nil), g.edges[from]...) }

// Layers returns the topological layers. Every node in a layer may run
// concurrently once all previous layers have completed.
func (g *Graph) Layers() ([][]string, error) {
	indeg := make(map[string]int, len(g.order))
	for _, id := range g.order {
		indeg[id] = g.indeg[id]
	}
	var layers [][]string
	frontier := g.zeroIndegree(indeg, nil)
	seen := 0
	for len(frontier) > 0 {
		layers = append(layers, frontier)
		next := map[string]struct{}{}
		for _, id := range frontier {
			seen++
			for _, to := range g.edges[id] {
				indeg[to]--
				if indeg[to] == 0 {
					next[to] = struct{}{}
				}
			}
		}
		frontier = g.zeroIndegree(indeg, next)
	}
	if seen != len(g.order) {
		path := g.cyclePath()
		return nil, &CycleError{Path: path}
	}
	return layers, nil
}

func (g *Graph) zeroIndegree(indeg map[string]int, pick map[string]struct{}) []string {
	var out []string
	for _, id := range g.order {
		if pick != nil {
			if _, ok := pick[id]; ok {
				out = append(out, id)
			}
			continue
		}
		if indeg[id] == 0 {
			out = append(out, id)
		}
	}
	return out
}

// CycleError carries a real closed cycle path found in the input.
type CycleError struct{ Path []string }

func (e *CycleError) Error() string {
	return fmt.Sprintf("%s: %v", ErrCycle, e.Path)
}

// Is lets errors.Is(err, ErrCycle) succeed.
func (e *CycleError) Is(target error) bool { return target == ErrCycle }

// cyclePath extracts one concrete closed path inside the remaining subgraph by
// DFS with parent pointers, closing the slice with the repeated node.
func (g *Graph) cyclePath() []string {
	color := map[string]uint8{} // 0 white, 1 gray, 2 black
	parent := map[string]string{}
	var stack []string
	var dfs func(string) []string
	dfs = func(u string) []string {
		color[u] = 1
		stack = append(stack, u)
		for _, v := range g.edges[u] {
			if color[v] == 0 {
				parent[v] = u
				if p := dfs(v); p != nil {
					return p
				}
			} else if color[v] == 1 {
				// back edge u -> v: walk parents from u until v
				path := []string{}
				cur := u
				for cur != v {
					path = append([]string{cur}, path...)
					cur = parent[cur]
				}
				return append([]string{v}, append(path, v)...)
			}
		}
		color[u] = 2
		return nil
	}
	for _, id := range g.order {
		if color[id] == 0 {
			if p := dfs(id); p != nil {
				return p
			}
		}
	}
	return nil
}
