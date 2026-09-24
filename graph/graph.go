// Package graph builds a task DAG and provides cycle detection and layering.
package graph

import (
	"slices"
	"strings"
)

// Graph is a directed graph of task IDs. An edge from->to means "to" depends
// on "from": "to" may only run after "from" has succeeded.
type Graph struct {
	nodes      map[string]bool
	deps       map[string]map[string]bool
	dependents map[string]map[string]bool
	edges      int
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{
		nodes:      map[string]bool{},
		deps:       map[string]map[string]bool{},
		dependents: map[string]map[string]bool{},
	}
}

// AddNode registers a task ID. Adding an existing ID is a no-op.
func (g *Graph) AddNode(id string) {
	if g.nodes[id] {
		return
	}
	g.nodes[id] = true
	g.deps[id] = map[string]bool{}
	g.dependents[id] = map[string]bool{}
}

// AddEdge records that "to" depends on "from". It is idempotent and returns
// an error if either endpoint is not a registered node.
func (g *Graph) AddEdge(from, to string) error {
	if !g.nodes[from] {
		return errUnknown(from)
	}
	if !g.nodes[to] {
		return errUnknown(to)
	}
	if g.deps[to][from] {
		return nil
	}
	g.deps[to][from] = true
	g.dependents[from][to] = true
	g.edges++
	return nil
}

type unknownError struct{ id string }

func errUnknown(id string) error { return &unknownError{id} }

func (e *unknownError) Error() string { return "graph: unknown node " + e.id }

// Nodes returns all task IDs in ascending order.
func (g *Graph) Nodes() []string {
	out := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// Edges returns the number of distinct dependency edges.
func (g *Graph) Edges() int { return g.edges }

// Dependencies returns the sorted IDs "id" directly depends on.
func (g *Graph) Dependencies(id string) []string { return sortedKeys(g.deps[id]) }

// Dependents returns the sorted IDs that directly depend on "id".
func (g *Graph) Dependents(id string) []string { return sortedKeys(g.dependents[id]) }

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// CycleError reports a dependency cycle. Path lists task IDs forming the
// cycle; every consecutive pair is an input edge and Path[0] == Path[last].
type CycleError struct{ Path []string }

func (e *CycleError) Error() string {
	return "graph: dependency cycle: " + strings.Join(e.Path, " -> ")
}

// FindCycle returns a real cycle path, or nil if the graph is acyclic.
// It uses iterative DFS with an explicit stack, so deep graphs are safe.
func (g *Graph) FindCycle() []string {
	const (
		white = iota // unvisited
		gray         // on the current DFS path
		black        // fully explored
	)
	color := map[string]int{}
	type frame struct {
		id   string
		next []string
	}
	for _, start := range g.Nodes() {
		if color[start] != white {
			continue
		}
		color[start] = gray
		stack := []frame{{id: start, next: g.Dependencies(start)}}
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			if len(top.next) == 0 {
				color[top.id] = black
				stack = stack[:len(stack)-1]
				continue
			}
			dep := top.next[0]
			top.next = top.next[1:]
			switch color[dep] {
			case white:
				color[dep] = gray
				stack = append(stack, frame{id: dep, next: g.Dependencies(dep)})
			case gray:
				path := []string{}
				for _, f := range stack {
					path = append(path, f.id)
				}
				path = path[slices.Index(path, dep):]
				return append(path, dep)
			}
		}
	}
	return nil
}

// Layers returns task IDs grouped into topological layers: every task in
// layer i depends only on tasks in earlier layers. IDs within a layer are
// sorted. It returns a *CycleError if the graph contains a cycle.
func (g *Graph) Layers() ([][]string, error) {
	if path := g.FindCycle(); len(path) > 0 {
		return nil, &CycleError{Path: path}
	}
	indeg := map[string]int{}
	var cur []string
	for _, id := range g.Nodes() {
		indeg[id] = len(g.deps[id])
		if indeg[id] == 0 {
			cur = append(cur, id)
		}
	}
	var layers [][]string
	for len(cur) > 0 {
		layers = append(layers, cur)
		var next []string
		for _, id := range cur {
			for _, d := range g.Dependents(id) {
				indeg[d]--
				if indeg[d] == 0 {
					next = append(next, d)
				}
			}
		}
		slices.Sort(next)
		cur = next
	}
	return layers, nil
}
