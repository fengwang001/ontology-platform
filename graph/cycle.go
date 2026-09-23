package graph

import (
	"errors"
)

// ErrCycle reports that the graph contains a directed cycle.
var ErrCycle = errors.New("graph: directed cycle detected")

// CycleError carries a real cycle path: consecutive elements name edges that
// all exist in the input, and the first element equals the last.
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string {
	msg := "graph: cycle: "
	for i, n := range e.Path {
		if i > 0 {
			msg += "->"
		}
		msg += n
	}
	return msg
}

func (e *CycleError) Is(target error) bool { return target == ErrCycle }

// Cycle returns a closed cycle path (path[0] == path[len-1], every adjacent
// pair is an input edge), or nil when the graph is acyclic. Self loops yield
// a two-element path [v, v].
func (g *Graph) Cycle() []string {
	const white, gray, black = 0, 1, 2
	color := map[string]int{}
	var stack []string
	index := map[string]int{}

	roots := sortedKeys(g.nodes)
	var dfs func(string) []string
	dfs = func(u string) []string {
		color[u] = gray
		index[u] = len(stack)
		stack = append(stack, u)
		for _, v := range g.Successors(u) {
			switch color[v] {
			case white:
				if p := dfs(v); p != nil {
					return p
				}
			case gray:
				path := append([]string{}, stack[index[v]:]...)
				return append(path, v) // close the loop
			}
		}
		stack = stack[:len(stack)-1]
		delete(index, u)
		color[u] = black
		return nil
	}

	for _, u := range roots {
		if color[u] == white {
			if p := dfs(u); p != nil {
				return p
			}
		}
	}
	return nil
}

// Check validates the graph, returning a *CycleError for any cycle.
func (g *Graph) Check() error {
	if p := g.Cycle(); p != nil {
		return &CycleError{Path: p}
	}
	return nil
}
