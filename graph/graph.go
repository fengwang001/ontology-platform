// Package graph builds and validates a DAG: cycle detection, in-degrees and
// topological layers. It depends on no other package in this module.
package graph

import (
	"errors"
	"fmt"
)

// ErrCycle is returned when the input contains a directed cycle.
var ErrCycle = errors.New("graph: cycle detected")

// ErrMissing is returned when an edge references an unknown step.
var ErrMissing = errors.New("graph: unknown step in edge")

// CycleError carries a real closed path found in the input, e.g. [a b c a].
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string { return fmt.Sprintf("cycle: %v", e.Path) }
func (e *CycleError) Unwrap() error { return ErrCycle }

// Graph is an immutable validated DAG.
type Graph struct {
	ids   []string
	succ  map[string][]string // edge from -> to  (from is a dependency of to)
	pred  map[string][]string
	indeg map[string]int
}

// New constructs a graph. ids are all steps; each edge [from,to] means "from
// must complete before to". Duplicate ids/edges are rejected.
func New(ids []string, edges [][2]string) (*Graph, error) {
	g := &Graph{
		succ:  map[string][]string{},
		pred:  map[string][]string{},
		indeg: map[string]int{},
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return nil, fmt.Errorf("graph: duplicate step %q", id)
		}
		seen[id] = true
		g.ids = append(g.ids, id)
		g.indeg[id] = 0
	}
	for _, e := range edges {
		if !seen[e[0]] || !seen[e[1]] {
			return nil, fmt.Errorf("%w: %v", ErrMissing, e)
		}
	}
	edgeSeen := map[[2]string]bool{}
	for _, e := range edges {
		if edgeSeen[e] {
			return nil, fmt.Errorf("graph: duplicate edge %v", e)
		}
		edgeSeen[e] = true
		g.succ[e[0]] = append(g.succ[e[0]], e[1])
		g.pred[e[1]] = append(g.pred[e[1]], e[0])
		g.indeg[e[1]]++
	}
	if p := findCycle(g); p != nil {
		return nil, &CycleError{Path: p}
	}
	return g, nil
}

// IDs returns steps in registration order.
func (g *Graph) IDs() []string { out := append([]string(nil), g.ids...); return out }

// InDegree returns the in-degree of a step (0 for unknown ids).
func (g *Graph) InDegree(id string) int { return g.indeg[id] }

// Layers returns topological layers: steps in the same layer have all
// dependencies already complete and are independent of one another.
func (g *Graph) Layers() [][]string {
	deg := map[string]int{}
	for id, d := range g.indeg {
		deg[id] = d
	}
	var layers [][]string
	remaining := len(g.ids)
	for remaining > 0 {
		var layer []string
		for _, id := range g.ids {
			if deg[id] == 0 {
				layer = append(layer, id)
			}
		}
		if len(layer) == 0 {
			return nil // cycle: unreachable for validated graphs
		}
		for _, id := range layer {
			deg[id] = -1
			remaining--
			for _, to := range g.succ[id] {
				deg[to]--
			}
		}
		layers = append(layers, layer)
	}
	return layers
}

// ReverseOrder returns every step id in reverse completion order: it is the
// reverse of the flattened topological layers, a valid strict compensation
// order for any execution prefix.
func (g *Graph) ReverseOrder() []string {
	layers := g.Layers()
	var out []string
	for i := len(layers) - 1; i >= 0; i-- {
		l := layers[i]
		for j := len(l) - 1; j >= 0; j-- {
			out = append(out, l[j])
		}
	}
	return out
}

// findCycle performs a DFS and returns one real closed path, nil if acyclic.
func findCycle(g *Graph) []string {
	const white, gray, black = 0, 1, 2
	color := map[string]int{}
	var stack []string
	var dfs func(string) []string
	dfs = func(u string) []string {
		color[u] = gray
		stack = append(stack, u)
		for _, v := range g.succ[u] {
			switch color[v] {
			case white:
				if p := dfs(v); p != nil {
					return p
				}
			case gray:
				for i, x := range stack {
					if x == v {
						path := append([]string(nil), stack[i:]...)
						return append(path, v) // closed path, first==last
					}
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[u] = black
		return nil
	}
	for _, id := range g.ids {
		if color[id] == white {
			if p := dfs(id); p != nil {
				return p
			}
		}
	}
	return nil
}
