// Package graph builds task DAGs, detects cycles, and computes topo layers.
package graph

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrUnknownTask is returned when an edge references a missing task.
var ErrUnknownTask = errors.New("graph: unknown task")

// ErrCycle is matched (via errors.Is) when the graph contains a cycle.
var ErrCycle = errors.New("graph: cycle detected")

// CycleError carries a real cycle path: every edge exists in the input and
// the path is closed (first == last).
type CycleError struct{ Path []string }

func (e *CycleError) Error() string {
	return fmt.Sprintf("%s: %s", ErrCycle, strings.Join(e.Path, " -> "))
}

func (e *CycleError) Is(target error) bool { return target == ErrCycle }

// Graph is a DAG of task IDs. Edge from->to means "to depends on from".
type Graph struct {
	nodes map[string]struct{}
	fwd   map[string]map[string]struct{}
	rev   map[string]map[string]struct{}
	edges int
}

func New() *Graph {
	return &Graph{
		nodes: map[string]struct{}{},
		fwd:   map[string]map[string]struct{}{},
		rev:   map[string]map[string]struct{}{},
	}
}

// AddTask registers a task ID (idempotent).
func (g *Graph) AddTask(id string) {
	if _, ok := g.nodes[id]; ok {
		return
	}
	g.nodes[id] = struct{}{}
	g.fwd[id] = map[string]struct{}{}
	g.rev[id] = map[string]struct{}{}
}

// AddEdge adds a dependency edge; duplicates are idempotent no-ops.
func (g *Graph) AddEdge(from, to string) error {
	for _, id := range []string{from, to} {
		if _, ok := g.nodes[id]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownTask, id)
		}
	}
	if _, ok := g.fwd[from][to]; ok {
		return nil
	}
	g.fwd[from][to] = struct{}{}
	g.rev[to][from] = struct{}{}
	g.edges++
	return nil
}

// Size returns node and edge counts.
func (g *Graph) Size() (nodes, edges int) { return len(g.nodes), g.edges }

// Tasks returns all task IDs in lexicographic order.
func (g *Graph) Tasks() []string { return sortedKeys(g.nodes) }

// Parents returns the sorted IDs of direct dependencies of id.
func (g *Graph) Parents(id string) []string { return sortedKeys(g.rev[id]) }

// Children returns the sorted IDs of direct dependents of id.
func (g *Graph) Children(id string) []string { return sortedKeys(g.fwd[id]) }

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Layers returns topo layers (independent tasks share a layer, IDs sorted).
// It returns a *CycleError with a real cycle path if the graph is cyclic.
func (g *Graph) Layers() ([][]string, error) {
	indeg := map[string]int{}
	for id := range g.nodes {
		indeg[id] = len(g.rev[id])
	}
	var cur []string
	for id, d := range indeg {
		if d == 0 {
			cur = append(cur, id)
		}
	}
	sort.Strings(cur)
	var layers [][]string
	done := 0
	for len(cur) > 0 {
		layers = append(layers, cur)
		done += len(cur)
		var next []string
		for _, id := range cur {
			for ch := range g.fwd[id] {
				indeg[ch]--
				if indeg[ch] == 0 {
					next = append(next, ch)
				}
			}
		}
		sort.Strings(next)
		cur = next
	}
	if done < len(g.nodes) {
		return nil, &CycleError{Path: g.findCycle(indeg)}
	}
	return layers, nil
}

// findCycle walks un-output parents (indeg > 0) until a node repeats, then
// reverses the segment into edge direction. Iterative: no stack growth.
func (g *Graph) findCycle(indeg map[string]int) []string {
	start := ""
	for id, d := range indeg {
		if d > 0 && (start == "" || id < start) {
			start = id
		}
	}
	pos := map[string]int{}
	var walk []string
	cur := start
	for {
		if i, ok := pos[cur]; ok {
			seg := walk[i:]
			path := make([]string, 0, len(seg)+1)
			path = append(path, cur)
			for j := len(seg) - 1; j >= 0; j-- {
				path = append(path, seg[j])
			}
			return path
		}
		pos[cur] = len(walk)
		walk = append(walk, cur)
		next := ""
		for p := range g.rev[cur] {
			if indeg[p] > 0 && (next == "" || p < next) {
				next = p
			}
		}
		cur = next
	}
}
