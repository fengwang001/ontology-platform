// Package graph builds and validates directed acyclic task graphs.
package graph

import (
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrNodeMissing is returned when an edge references an unknown node.
	ErrNodeMissing = errors.New("graph: edge references missing node")
	// ErrCycle is returned when the graph contains a directed cycle.
	ErrCycle = errors.New("graph: cycle detected")
)

// CycleError carries a real closed path: the first and last node are equal.
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string { return fmt.Sprintf("graph: cycle: %v", e.Path) }
func (e *CycleError) Is(target error) bool { return target == ErrCycle }

// Graph is a directed graph. Edge(from,to) means "to depends on from".
type Graph struct {
	nodes map[string]struct{}
	succ  map[string]map[string]struct{}
	pred  map[string]map[string]struct{}
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{
		nodes: map[string]struct{}{},
		succ:  map[string]map[string]struct{}{},
		pred:  map[string]map[string]struct{}{},
	}
}

// AddNode registers a task id. It is idempotent.
func (g *Graph) AddNode(id string) {
	g.nodes[id] = struct{}{}
	if g.succ[id] == nil {
		g.succ[id] = map[string]struct{}{}
	}
	if g.pred[id] == nil {
		g.pred[id] = map[string]struct{}{}
	}
}

// AddEdge records that "to" depends on "from". Duplicate edges are idempotent.
func (g *Graph) AddEdge(from, to string) error {
	if _, ok := g.nodes[from]; !ok {
		return fmt.Errorf("%w: %q", ErrNodeMissing, from)
	}
	if _, ok := g.nodes[to]; !ok {
		return fmt.Errorf("%w: %q", ErrNodeMissing, to)
	}
	g.succ[from][to] = struct{}{}
	g.pred[to][from] = struct{}{}
	return nil
}

// HasNode reports whether id was added.
func (g *Graph) HasNode(id string) bool { _, ok := g.nodes[id]; return ok }

// Nodes returns all node ids in lexicographic order.
func (g *Graph) Nodes() []string {
	ids := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Successors returns the sorted ids that depend on id.
func (g *Graph) Successors(id string) []string {
	out := make([]string, 0, len(g.succ[id]))
	for s := range g.succ[id] {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Predecessors returns the sorted ids that id depends on.
func (g *Graph) Predecessors(id string) []string {
	out := make([]string, 0, len(g.pred[id]))
	for p := range g.pred[id] {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (g *Graph) ensureMaps() {
	for id := range g.nodes {
		if g.succ[id] == nil {
			g.succ[id] = map[string]struct{}{}
		}
		if g.pred[id] == nil {
			g.pred[id] = map[string]struct{}{}
		}
	}
}

// Cycle returns a closed path whose edges all exist, or nil if the graph is acyclic.
func (g *Graph) Cycle() []string {
	g.ensureMaps()
	const white, gray, black = 0, 1, 2
	color := map[string]int{}
	parent := map[string]string{}
	var dfs func(string) []string
	dfs = func(u string) []string {
		color[u] = gray
		next := g.Successors(u)
		for _, v := range next {
			if color[v] == white {
				parent[v] = u
				if p := dfs(v); p != nil {
					return p
				}
			} else if color[v] == gray {
				path := []string{v}
				for x := u; x != v; x = parent[x] {
					path = append(path, x)
					if _, ok := parent[x]; !ok {
						break
					}
				}
				for i, j := 1, len(path)-1; i < j; i, j = i+1, j-1 {
					path[i], path[j] = path[j], path[i]
				}
				path = append(path, v)
				return path
			}
		}
		color[u] = black
		return nil
	}
	for _, id := range g.Nodes() {
		if color[id] == white {
			if p := dfs(id); p != nil {
				return p
			}
		}
	}
	return nil
}

// Layers returns Kahn topologically sorted layers, each layer sorted by id.
// It returns ErrCycle for cyclic graphs.
func (g *Graph) Layers() ([][]string, error) {
	g.ensureMaps()
	indeg := map[string]int{}
	ready := []string{}
	for id := range g.nodes {
		indeg[id] = len(g.pred[id])
		if indeg[id] == 0 {
			ready = append(ready, id)
		}
	}
	layers := [][]string{}
	seen := 0
	for len(ready) > 0 {
		sort.Strings(ready)
		layer := append([]string(nil), ready...)
		layers = append(layers, layer)
		next := []string{}
		for _, u := range layer {
			seen++
			for _, v := range g.Successors(u) {
				indeg[v]--
				if indeg[v] == 0 {
					next = append(next, v)
				}
			}
		}
		ready = next
	}
	if seen != len(g.nodes) {
		if p := g.Cycle(); p != nil {
			return nil, &CycleError{Path: p}
		}
		return nil, ErrCycle
	}
	return layers, nil
}
