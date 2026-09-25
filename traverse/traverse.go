// Package traverse produces a node visit order over a directed graph.
//
// BFS is used deliberately: the BFS depth of a node is exactly its shortest
// edge distance from the start, which gives the downstream depth bound an
// unambiguous meaning.
package traverse

import (
	"errors"

	"ontology/graph"
)

type Dir int

const (
	DirOut  Dir = iota + 1 // follow outgoing edges
	DirIn                  // follow incoming edges
	DirBoth                // follow edges in both directions
)

var (
	ErrStartNotFound = errors.New("traverse: start node not found")
	ErrInvalidDir    = errors.New("traverse: invalid direction")
)

func (d Dir) Valid() bool { return d == DirOut || d == DirIn || d == DirBoth }

// Neighbors returns the nodes adjacent to node under dir, in insertion order
// (outgoing first for DirBoth). It is shared by higher-level traversal code.
func Neighbors(g *graph.Graph, node string, dir Dir) []string {
	switch dir {
	case DirOut:
		return g.Out(node)
	case DirIn:
		return g.In(node)
	case DirBoth:
		return append(append([]string{}, g.Out(node)...), g.In(node)...)
	default:
		return nil
	}
}

// Walk returns every node reachable from start via dir, in BFS order, starting
// with start itself. Each node is reported exactly once, at its first visit.
func Walk(g *graph.Graph, start string, dir Dir) ([]string, error) {
	if !g.HasNode(start) {
		return nil, ErrStartNotFound
	}
	if !dir.Valid() {
		return nil, ErrInvalidDir
	}

	visited := map[string]bool{start: true}
	order := []string{start}
	queue := []string{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range Neighbors(g, current, dir) {
			if visited[next] {
				continue
			}
			visited[next] = true
			order = append(order, next)
			queue = append(queue, next)
		}
	}
	return order, nil
}
