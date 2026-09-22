// Package graph builds and validates directed acyclic graphs of workflow
// steps. It has no dependencies on other packages in this module.
package graph

import (
	"errors"
	"fmt"
	"strings"
)

// ErrDuplicateNode is returned when a node ID is added twice.
var ErrDuplicateNode = errors.New("graph: duplicate node")

// ErrUnknownNode is returned when an edge references a missing node.
var ErrUnknownNode = errors.New("graph: unknown node")

// CycleError reports a dependency cycle. Path is a real closed walk taken
// from the user supplied edges: every consecutive pair is an input edge and
// the first and last elements are equal.
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string {
	return "graph: cycle detected: " + strings.Join(e.Path, " -> ")
}

// Graph is a directed acyclic graph under construction. AddEdge(from, to)
// means "from must complete before to starts" (to depends on from).
type Graph struct {
	order []string // node IDs in insertion order, for determinism
	known map[string]bool
	succ  map[string][]string
	indeg map[string]int
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{
		known: make(map[string]bool),
		succ:  make(map[string][]string),
		indeg: make(map[string]int),
	}
}

// AddNode registers a step ID. Duplicate IDs are rejected.
func (g *Graph) AddNode(id string) error {
	if g.known[id] {
		return fmt.Errorf("%w: %q", ErrDuplicateNode, id)
	}
	g.known[id] = true
	g.order = append(g.order, id)
	return nil
}

// AddEdge records that "from" must finish before "to". Both endpoints must
// already be registered. A self-loop is reported as a cycle immediately.
func (g *Graph) AddEdge(from, to string) error {
	if !g.known[from] {
		return fmt.Errorf("%w: %q", ErrUnknownNode, from)
	}
	if !g.known[to] {
		return fmt.Errorf("%w: %q", ErrUnknownNode, to)
	}
	if from == to {
		return &CycleError{Path: []string{from, to}}
	}
	g.succ[from] = append(g.succ[from], to)
	g.indeg[to]++
	return nil
}

// Nodes returns all node IDs in insertion order.
func (g *Graph) Nodes() []string {
	out := make([]string, len(g.order))
	copy(out, g.order)
	return out
}

// Len returns the number of nodes.
func (g *Graph) Len() int { return len(g.order) }

// Validate checks acyclicity with a three-color DFS. On a back edge it
// reconstructs the closed cycle from the current recursion stack, so the
// reported path consists solely of edges present in the input.
func (g *Graph) Validate() error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(g.order))
	var stack []string
	var visit func(id string) error
	visit = func(id string) error {
		color[id] = gray
		stack = append(stack, id)
		for _, next := range g.succ[id] {
			switch color[next] {
			case gray:
				start := 0
				for i, s := range stack {
					if s == next {
						start = i
						break
					}
				}
				path := append([]string{}, stack[start:]...)
				path = append(path, next)
				return &CycleError{Path: path}
			case white:
				if err := visit(next); err != nil {
					return err
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
		return nil
	}
	for _, id := range g.order {
		if color[id] == white {
			if err := visit(id); err != nil {
				return err
			}
		}
	}
	return nil
}
