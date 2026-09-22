package graph

import (
	"errors"
	"fmt"
)

// ErrCycle is returned when the input graph contains a directed cycle.
// A real closed path that exists in the input is attached via CyclePath.
type CycleError struct {
	Path []string // closed walk: first node is repeated as the last node
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("graph: cycle detected: %v", e.Path)
}

// AsCycle extracts a *CycleError from err, if present.
func AsCycle(err error) (*CycleError, bool) {
	var ce *CycleError
	if errors.As(err, &ce) {
		return ce, true
	}
	return nil, false
}

// Builder accumulates nodes and dependency edges before validation.
// An edge u -> v means "u must complete before v can start".
type Builder struct {
	nodes    []string
	known    map[string]bool
	edges    map[string][]string
	edgeSeen map[[2]string]bool
}

// NewBuilder returns an empty builder.
func NewBuilder() *Builder {
	return &Builder{
		known:    map[string]bool{},
		edges:    map[string][]string{},
		edgeSeen: map[[2]string]bool{},
	}
}

// AddNode registers a step id. Duplicate registration is rejected.
func (b *Builder) AddNode(id string) error {
	if id == "" {
		return errors.New("graph: node id must not be empty")
	}
	if b.known[id] {
		return fmt.Errorf("graph: duplicate node %q", id)
	}
	b.known[id] = true
	b.nodes = append(b.nodes, id)
	return nil
}

// AddEdge records that from is a prerequisite of to.
func (b *Builder) AddEdge(from, to string) error {
	if from == to {
		return &CycleError{Path: []string{from, from}}
	}
	if !b.known[from] {
		return fmt.Errorf("graph: unknown edge source %q", from)
	}
	if !b.known[to] {
		return fmt.Errorf("graph: unknown edge target %q", to)
	}
	key := [2]string{from, to}
	if b.edgeSeen[key] {
		return fmt.Errorf("graph: duplicate edge %q -> %q", from, to)
	}
	b.edgeSeen[key] = true
	b.edges[from] = append(b.edges[from], to)
	return nil
}

// Graph is an immutable, validated DAG.
type Graph struct {
	order    []string // insertion order of nodes
	index    map[string]int
	indeg    map[string]int
	out      map[string][]string
	in       map[string][]string
	layers   [][]string
	topo     []string // deterministic topological order (within layers, insertion order)
	maxWidth int
}

// Build validates the graph (acyclicity, endpoint existence) and freezes it.
func (b *Builder) Build() (*Graph, error) {
	g := &Graph{
		order: append([]string(nil), b.nodes...),
		index: make(map[string]int, len(b.nodes)),
		indeg: make(map[string]int, len(b.nodes)),
		out:   make(map[string][]string, len(b.nodes)),
		in:    make(map[string][]string, len(b.nodes)),
	}
	for i, id := range g.order {
		g.index[id] = i
		g.indeg[id] = 0
	}
	for from, tos := range b.edges {
		for _, to := range tos {
			g.out[from] = append(g.out[from], to)
			g.in[to] = append(g.in[to], from)
			g.indeg[to]++
		}
	}
	if cycle := findCycle(g); cycle != nil {
		return nil, &CycleError{Path: cycle}
	}
	g.layers = kahnLayers(g)
	for _, l := range g.layers {
		g.topo = append(g.topo, l...)
		if len(l) > g.maxWidth {
			g.maxWidth = len(l)
		}
	}
	return g, nil
}

// findCycle runs DFS three-coloring and returns one real closed path if any.
func findCycle(g *Graph) []string {
	const white, gray, black = 0, 1, 2
	color := make(map[string]int, len(g.order))
	stack := []string{}
	onStack := map[string]int{} // node -> position in stack
	var dfs func(string) []string
	dfs = func(u string) []string {
		color[u] = gray
		onStack[u] = len(stack)
		stack = append(stack, u)
		for _, v := range g.out[u] {
			switch color[v] {
			case white:
				if p := dfs(v); p != nil {
					return p
				}
			case gray:
				// Real closed walk: v ... u -> v, with v repeated at the end.
				path := append([]string(nil), stack[onStack[v]:]...)
				return append(path, v)
			}
		}
		stack = stack[:len(stack)-1]
		delete(onStack, u)
		color[u] = black
		return nil
	}
	for _, u := range g.order {
		if color[u] == white {
			if p := dfs(u); p != nil {
				return p
			}
		}
	}
	return nil
}

// kahnLayers groups nodes into dependency levels; every node in layer k has
// all its predecessors in earlier layers.
func kahnLayers(g *Graph) [][]string {
	indeg := make(map[string]int, len(g.order))
	ready := make([]string, 0)
	for _, id := range g.order {
		indeg[id] = g.indeg[id]
		if indeg[id] == 0 {
			ready = append(ready, id)
		}
	}
	var layers [][]string
	seen := 0
	for len(ready) > 0 {
		layer := append([]string(nil), ready...)
		layers = append(layers, layer)
		next := make([]string, 0)
		for _, u := range layer {
			seen++
			for _, v := range g.out[u] {
				indeg[v]--
				if indeg[v] == 0 {
					next = append(next, v)
				}
			}
		}
		ready = next
	}
	if seen != len(g.order) {
		panic("graph: cycle survived validation")
	}
	return layers
}

// Layers returns the topological layers (widest first scheduling view).
func (g *Graph) Layers() [][]string {
	out := make([][]string, len(g.layers))
	for i, l := range g.layers {
		out[i] = append([]string(nil), l...)
	}
	return out
}

// TopoOrder returns one deterministic topological ordering.
func (g *Graph) TopoOrder() []string { return append([]string(nil), g.topo...) }

// ReverseTopoOrder returns the compensation order (dependents first).
func (g *Graph) ReverseTopoOrder() []string {
	out := g.TopoOrder()
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Nodes returns all node ids in insertion order.
func (g *Graph) Nodes() []string { return append([]string(nil), g.order...) }

// Has reports whether id belongs to the graph.
func (g *Graph) Has(id string) bool { _, ok := g.index[id]; return ok }

// MaxWidth returns the size of the widest topological layer.
func (g *Graph) MaxWidth() int { return g.maxWidth }

// Len returns the node count.
func (g *Graph) Len() int { return len(g.order) }
