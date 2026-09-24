// Package graph builds directed acyclic task graphs, validates them
// (unknown references, cycles including self loops) and produces
// dependency layers via Kahn's algorithm.
package graph

import (
	"fmt"
	"sort"

	"ontology/fail"
)

// Graph is a directed graph: edge from -> to means "to depends on from",
// i.e. from must finish before to may start.
type Graph struct {
	nodes map[string]struct{}
	out   map[string]map[string]struct{} // from -> successors
	in    map[string]map[string]struct{} // to -> predecessors
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{
		nodes: map[string]struct{}{},
		out:   map[string]map[string]struct{}{},
		in:    map[string]map[string]struct{}{},
	}
}

// Add registers one or more task IDs.
func (g *Graph) Add(ids ...string) {
	for _, id := range ids {
		if _, ok := g.nodes[id]; !ok {
			g.nodes[id] = struct{}{}
			g.out[id] = map[string]struct{}{}
			g.in[id] = map[string]struct{}{}
		}
	}
}

// AddEdge records that task to depends on from. Duplicate edges are
// idempotent. Referencing an unknown task fails with fail.ErrNoSuchTask.
func (g *Graph) AddEdge(from, to string) error {
	if !g.Has(from) {
		return fmt.Errorf("%w: %q", fail.ErrNoSuchTask, from)
	}
	if !g.Has(to) {
		return fmt.Errorf("%w: %q", fail.ErrNoSuchTask, to)
	}
	g.out[from][to] = struct{}{}
	g.in[to][from] = struct{}{}
	return nil
}

// MustAddEdge is AddEdge that panics on error.
func (g *Graph) MustAddEdge(from, to string) {
	if err := g.AddEdge(from, to); err != nil {
		panic(err)
	}
}

// Nodes returns all task IDs sorted ascending.
func (g *Graph) Nodes() []string {
	ids := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Has reports whether id is a registered task.
func (g *Graph) Has(id string) bool {
	_, ok := g.nodes[id]
	return ok
}

// HasEdge reports whether a distinct edge from->to exists.
func (g *Graph) HasEdge(from, to string) bool {
	_, ok := g.out[from][to]
	return ok
}

// CheckReferences verifies every edge endpoint exists; it always passes
// for graphs built via AddEdge and exists for explicit pre-validation.
func (g *Graph) CheckReferences() error {
	for id := range g.nodes {
		for s := range g.out[id] {
			if !g.Has(s) {
				return fmt.Errorf("%w: %q", fail.ErrNoSuchTask, s)
			}
		}
	}
	return nil
}

// Cycle returns a real closed path (each consecutive pair is an input
// edge, last node equals the first) and fail.ErrCyclic when the graph has
// a cycle. Self loops are detected. It returns nil error for DAGs.
func (g *Graph) Cycle() ([]string, error) {
	const white, gray, black = 0, 1, 2
	color := map[string]int{}
	parent := map[string]string{}
	for _, start := range g.Nodes() {
		if color[start] != white {
			continue
		}
		color[start] = gray
		type frame struct {
			id   string
			succ []string
			idx  int
		}
		stack := []frame{{start, g.sortedOut(start), 0}}
		for len(stack) > 0 {
			fp := &stack[len(stack)-1]
			if fp.idx >= len(fp.succ) {
				color[fp.id] = black
				stack = stack[:len(stack)-1]
				continue
			}
			next := fp.succ[fp.idx]
			fp.idx++
			switch color[next] {
			case gray:
				path := []string{}
				for cur := fp.id; ; cur = parent[cur] {
					path = append(path, cur)
					if cur == next {
						break
					}
				}
				reverse(path)
				path = append(path, next)
				return path, fmt.Errorf("%w: %v", fail.ErrCyclic, path)
			case white:
				color[next] = gray
				parent[next] = fp.id
				stack = append(stack, frame{next, g.sortedOut(next), 0})
			}
		}
	}
	return nil, nil
}

func (g *Graph) sortedOut(id string) []string {
	out := make([]string, 0, len(g.out[id]))
	for s := range g.out[id] {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func reverse(s []string) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// Layers returns topological layers: layer 0 holds tasks without
// predecessors, and every task in layer k depends only on earlier layers.
// The graph must be acyclic.
func (g *Graph) Layers() ([][]string, error) {
	if _, err := g.Cycle(); err != nil {
		return nil, err
	}
	remaining := map[string]int{}
	for id := range g.nodes {
		remaining[id] = len(g.in[id])
	}
	var layers [][]string
	frontier := g.zeroRemaining(remaining)
	for len(frontier) > 0 {
		layers = append(layers, frontier)
		for _, id := range frontier {
			for s := range g.out[id] {
				remaining[s]--
			}
		}
		frontier = g.zeroRemaining(remaining)
	}
	return layers, nil
}

func (g *Graph) zeroRemaining(remaining map[string]int) []string {
	var ready []string
	for id, n := range remaining {
		if n == 0 {
			ready = append(ready, id)
			delete(remaining, id)
		}
	}
	sort.Strings(ready)
	return ready
}

// Edges reports the total number of distinct edges.
func (g *Graph) Edges() int {
	n := 0
	for id := range g.nodes {
		n += len(g.out[id])
	}
	return n
}
