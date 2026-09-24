// Package graph builds a directed acyclic task graph, validates it and
// computes deterministic topological layers.
package graph

import (
	"errors"
	"fmt"
	"sort"
)

// ErrUnknownDep is returned (wrapped) when an edge references a missing task.
var ErrUnknownDep = errors.New("graph: edge references unknown task")

// CycleError carries a real closed walk: consecutive pairs in Path are edges
// present in the input, and the first and last elements are identical.
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("graph: cycle detected: %v", e.Path)
}

// Graph is a directed graph. An edge from -> to means from is a prerequisite
// of to and must finish first.
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

// AddTask registers a task id. Registering twice is harmless.
func (g *Graph) AddTask(id string) {
	g.nodes[id] = struct{}{}
	if g.succ[id] == nil {
		g.succ[id] = map[string]struct{}{}
	}
	if g.pred[id] == nil {
		g.pred[id] = map[string]struct{}{}
	}
}

// AddEdge records a prerequisite edge. Duplicate edges are idempotent.
// Both endpoints must already be registered, otherwise the returned error
// wraps ErrUnknownDep.
func (g *Graph) AddEdge(from, to string) error {
	if _, ok := g.nodes[from]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownDep, from)
	}
	if _, ok := g.nodes[to]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownDep, to)
	}
	if _, ok := g.succ[from][to]; ok {
		return nil
	}
	g.succ[from][to] = struct{}{}
	g.pred[to][from] = struct{}{}
	return nil
}

func (g *Graph) sortedKeys(m map[string]struct{}) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// IDs returns all task ids in ascending order.
func (g *Graph) IDs() []string { return g.sortedKeys(g.nodes) }

// Succs returns the sorted successors of id.
func (g *Graph) Succs(id string) []string { return g.sortedKeys(g.succ[id]) }

// Validate checks referenced endpoints and detects cycles, including
// self loops. The reported cycle path is deterministic regardless of the
// order edges were added.
func (g *Graph) Validate() error {
	if err := g.findCycle(); err != nil {
		return err
	}
	return nil
}

func (g *Graph) findCycle() error {
	const white, colGray, black = 0, 1, 2
	color := map[string]int{}
	type frame struct {
		id  string
		idx int
	}
	for _, root := range g.IDs() {
		if color[root] != white {
			continue
		}
		pathStack := []string{}
		stack := []frame{{id: root}}
		color[root] = colGray
		pathStack = append(pathStack, root)
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			next := g.Succs(top.id)
			if top.idx >= len(next) {
				color[top.id] = black
				stack = stack[:len(stack)-1]
				pathStack = pathStack[:len(pathStack)-1]
				continue
			}
			u := next[top.idx]
			top.idx++
			switch color[u] {
			case white:
				color[u] = colGray
				pathStack = append(pathStack, u)
				stack = append(stack, frame{id: u})
			case colGray:
				for i, id := range pathStack {
					if id == u {
						path := append([]string{}, pathStack[i:]...)
						path = append(path, u)
						return &CycleError{Path: path}
					}
				}
			}
		}
	}
	return nil
}

// Layers returns topological layers. Every node in layer i has all
// predecessors in earlier layers. Within a layer ids are ascending.
func (g *Graph) Layers() ([][]string, error) {
	if err := g.findCycle(); err != nil {
		return nil, err
	}
	indeg := make(map[string]int, len(g.nodes))
	for id := range g.nodes {
		indeg[id] = len(g.pred[id])
	}
	ready := []string{}
	for id, d := range indeg {
		if d == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	layers := [][]string{}
	seen := 0
	for len(ready) > 0 {
		layer := append([]string{}, ready...)
		layers = append(layers, layer)
		seen += len(layer)
		next := []string{}
		for _, id := range layer {
			for _, s := range g.Succs(id) {
				indeg[s]--
				if indeg[s] == 0 {
					next = append(next, s)
				}
			}
		}
		sort.Strings(next)
		ready = next
	}
	if seen != len(g.nodes) {
		return nil, &CycleError{}
	}
	return layers, nil
}
