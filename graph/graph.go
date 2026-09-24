package graph

import (
	"errors"
	"fmt"
	"sort"
)

var (
	ErrUnknownDependency = errors.New("graph: edge references unknown node")
	ErrCycle             = errors.New("graph: cycle detected")
)

// CycleError describes a real closed path: [v0, v1, ..., v0],
// every consecutive edge exists in the input.
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("%v: %v", ErrCycle, e.Path)
}
func (e *CycleError) Unwrap() error { return ErrCycle }

// Graph is a DAG candidate: edge from dep -> task means dep runs first.
type Graph struct {
	nodes map[string]struct{}
	succ  map[string]map[string]struct{}
	pred  map[string]map[string]struct{}
}

func New() *Graph {
	return &Graph{
		nodes: map[string]struct{}{},
		succ:  map[string]map[string]struct{}{},
		pred:  map[string]map[string]struct{}{},
	}
}

func (g *Graph) AddNode(id string) {
	if _, ok := g.nodes[id]; ok {
		return
	}
	g.nodes[id] = struct{}{}
	g.succ[id] = map[string]struct{}{}
	g.pred[id] = map[string]struct{}{}
}

// AddEdge records dep -> task. Both nodes must already be registered;
// otherwise ErrUnknownDependency is returned. Duplicate edges are idempotent.
func (g *Graph) AddEdge(dep, task string) error {
	if !g.HasNode(dep) {
		return &unknownDepError{dep: dep, task: task}
	}
	if !g.HasNode(task) {
		return &unknownDepError{dep: dep, task: task}
	}
	if _, ok := g.succ[dep][task]; ok {
		return nil
	}
	g.succ[dep][task] = struct{}{}
	g.pred[task][dep] = struct{}{}
	return nil
}

func (g *Graph) Nodes() []string {
	out := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		out = append(out, id)
	}
	return out
}

func (g *Graph) HasNode(id string) bool {
	_, ok := g.nodes[id]
	return ok
}

func (g *Graph) Succ(id string) []string {
	out := make([]string, 0, len(g.succ[id]))
	for s := range g.succ[id] {
		out = append(out, s)
	}
	return out
}

func (g *Graph) Pred(id string) []string {
	out := make([]string, 0, len(g.pred[id]))
	for p := range g.pred[id] {
		out = append(out, p)
	}
	return out
}

type unknownDepError struct{ dep, task string }

func (e *unknownDepError) Error() string {
	return fmt.Sprintf("%v: %q required by %q", ErrUnknownDependency, e.dep, e.task)
}
func (e *unknownDepError) Unwrap() error { return ErrUnknownDependency }

// Validate checks for cycles (including self loops).
func (g *Graph) Validate() error { return g.checkCycle() }

// checkCycle uses iterative three-color DFS and returns a closed path.
func (g *Graph) checkCycle() error {
	const white, gray, black = 0, 1, 2
	color := map[string]int{}
	var stack []frame
	for start := range g.nodes {
		if color[start] != white {
			continue
		}
		color[start] = gray
		stack = append(stack, frame{node: start, next: 0})
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			kids := sortedKeys(g.succ[top.node])
			if top.next >= len(kids) {
				color[top.node] = black
				stack = stack[:len(stack)-1]
				continue
			}
			child := kids[top.next]
			top.next++
			switch color[child] {
			case white:
				color[child] = gray
				stack = append(stack, frame{node: child, next: 0})
			case gray:
				idx := 0
				for k := range stack {
					if stack[k].node == child {
						idx = k
					}
				}
				path := []string{}
				for k := idx; k < len(stack); k++ {
					path = append(path, stack[k].node)
				}
				path = append(path, child)
				return &CycleError{Path: path}
			}
		}
	}
	return nil
}

type frame struct {
	node string
	next int
}

// Layers returns topological layers (Kahn), deterministic within a layer.
func (g *Graph) Layers() ([][]string, error) {
	if err := g.checkCycle(); err != nil {
		return nil, err
	}
	indeg := map[string]int{}
	for id := range g.nodes {
		indeg[id] = len(g.pred[id])
	}
	var layers [][]string
	remaining := len(g.nodes)
	for remaining > 0 {
		var layer []string
		for id := range g.nodes {
			if indeg[id] == 0 {
				layer = append(layer, id)
			}
		}
		if len(layer) == 0 {
			return nil, &CycleError{}
		}
		sort.Strings(layer)
		for _, id := range layer {
			indeg[id] = -1
			remaining--
			for s := range g.succ[id] {
				indeg[s]--
			}
		}
		layers = append(layers, layer)
	}
	return layers, nil
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
