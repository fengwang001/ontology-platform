// Package graph builds and validates directed acyclic graphs of workflow
// steps. It depends on no other package in this module.
package graph

import (
	"errors"
	"fmt"
)

// CycleError reports that the input graph contains a directed cycle. Cycle is
// an ordered list of step IDs forming a closed walk: it starts and ends with
// the same node and every consecutive pair is a real edge of the input graph.
type CycleError struct {
	Cycle []string
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("graph: cycle detected: %v", e.Cycle)
}

// ErrUnknownStep is returned when an edge references an unregistered step.
var ErrUnknownStep = errors.New("graph: unknown step")

// Graph is an immutable validated DAG.
type Graph struct {
	// ids preserves user-insertion order for deterministic output.
	ids   []string
	succ  map[string][]string
	pred  map[string][]string
	indeg map[string]int
}

// Builder constructs a graph incrementally.
type Builder struct {
	ids    []string
	succ   map[string]map[string]struct{}
	pred   map[string]map[string]struct{}
	exists map[string]struct{}
}

// NewBuilder returns an empty builder.
func NewBuilder() *Builder {
	return &Builder{
		succ:   map[string]map[string]struct{}{},
		pred:   map[string]map[string]struct{}{},
		exists: map[string]struct{}{},
	}
}

// AddStep registers a step. Duplicate registration is a no-op.
func (b *Builder) AddStep(id string) {
	if _, ok := b.exists[id]; ok {
		return
	}
	b.exists[id] = struct{}{}
	b.ids = append(b.ids, id)
	b.succ[id] = map[string]struct{}{}
	b.pred[id] = map[string]struct{}{}
}

// AddEdge records that step from must finish before step to may run. The edge
// direction is the scheduling direction (from -> to). Duplicate edges are
// deduplicated.
func (b *Builder) AddEdge(from, to string) error {
	if _, ok := b.exists[from]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownStep, from)
	}
	if _, ok := b.exists[to]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownStep, to)
	}
	b.succ[from][to] = struct{}{}
	b.pred[to][from] = struct{}{}
	return nil
}

func sortedKeys(m map[string]struct{}, order []string) []string {
	var out []string
	for _, id := range order {
		if _, ok := m[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

// Build freezes the builder and validates the graph. It returns *CycleError
// when a directed cycle exists. The returned graph must not be modified even
// if validation fails to build one.
func (b *Builder) Build() (*Graph, error) {
	g := &Graph{
		ids:   append([]string(nil), b.ids...),
		succ:  make(map[string][]string, len(b.ids)),
		pred:  make(map[string][]string, len(b.ids)),
		indeg: make(map[string]int, len(b.ids)),
	}
	for _, id := range g.ids {
	g.succ[id] = sortedKeys(b.succ[id], g.ids)
	g.pred[id] = sortedKeys(b.pred[id], g.ids)
	g.indeg[id] = len(b.pred[id])
	}
	if cyc := findCycle(g); cyc != nil {
		return nil, &CycleError{Cycle: cyc}
	}
	return g, nil
}

// findCycle performs an iterative DFS in insertion order and returns a real
// closed path (first node repeated at the end), or nil for an acyclic graph.
func findCycle(g *Graph) []string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var stack []string
	edgeAt := map[string]int{}

	for _, start := range g.ids {
		if color[start] != white {
			continue
		}
		color[start] = gray
		stack = append(stack, start)
		edgeAt[start] = 0
		for len(stack) > 0 {
			node := stack[len(stack)-1]
			nexts := g.succ[node]
			if edgeAt[node] < len(nexts) {
				child := nexts[edgeAt[node]]
				edgeAt[node]++
				switch color[child] {
				case gray:
					return closedPath(stack, child)
				case white:
					color[child] = gray
					stack = append(stack, child)
					edgeAt[child] = 0
				}
				continue
			}
			color[node] = black
			stack = stack[:len(stack)-1]
			delete(edgeAt, node)
		}
	}
	return nil
}

// closedPath extracts the cycle suffix of the DFS stack starting at target and
// appends target again so the result is visibly closed.
func closedPath(stack []string, target string) []string {
	for i, id := range stack {
		if id == target {
			cyc := append([]string(nil), stack[i:]...)
			return append(cyc, target)
		}
	}
	return []string{target, target}
}

// IDs returns all step IDs in insertion order.
func (g *Graph) IDs() []string { return append([]string(nil), g.ids...) }

// InDegree returns the number of prerequisites (incoming scheduling edges) of
// id.
func (g *Graph) InDegree(id string) int { return g.indeg[id] }

// Dependents returns IDs directly unlocked by id (outgoing edges).
func (g *Graph) Dependents(id string) []string {
	return append([]string(nil), g.succ[id]...)
}

// Dependencies returns the direct prerequisites of id (incoming edges).
func (g *Graph) Dependencies(id string) []string {
	return append([]string(nil), g.pred[id]...)
}
