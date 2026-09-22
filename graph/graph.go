// Package graph builds and validates directed acyclic graphs.
//
// An edge from p (predecessor/dependency) to s (successor) means "p must
// complete before s can run". Validation rejects unknown node references and
// cycles; a real closed cycle path is reported with CycleError.
package graph

import "errors"

// ErrDuplicateNode is returned when a node ID is added twice.
var ErrDuplicateNode = errors.New("graph: duplicate node")

// ErrUnknownNode is returned when an edge references a missing node.
var ErrUnknownNode = errors.New("graph: unknown node")

// CycleError reports a cycle that really exists in the input. Path is a
// closed walk of node IDs: Path[0] == Path[len(Path)-1].
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string {
	return "graph: cycle detected: " + join(e.Path)
}

func join(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += " -> "
		}
		out += id
	}
	return out
}

// Graph is an immutable-once-built DAG of step IDs.
type Graph struct {
	nodes []string
	succ  map[string][]string // dependency -> dependents
	pred  map[string][]string // dependent -> dependencies
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{
		succ: map[string][]string{},
		pred: map[string][]string{},
	}
}

// AddNode registers an ID and returns ErrDuplicateNode if it already exists.
func (g *Graph) AddNode(id string) error {
	if _, ok := g.succ[id]; ok {
		return ErrDuplicateNode
	}
	g.nodes = append(g.nodes, id)
	g.succ[id] = nil
	g.pred[id] = nil
	return nil
}

// AddEdge records that predecessor must finish before successor.
func (g *Graph) AddEdge(predecessor, successor string) error {
	if _, ok := g.succ[predecessor]; !ok {
		return ErrUnknownNode
	}
	if _, ok := g.succ[successor]; !ok {
		return ErrUnknownNode
	}
	g.succ[predecessor] = append(g.succ[predecessor], successor)
	g.pred[successor] = append(g.pred[successor], predecessor)
	return nil
}

// Nodes returns all node IDs in insertion order.
func (g *Graph) Nodes() []string {
	out := make([]string, len(g.nodes))
	copy(out, g.nodes)
	return out
}

// Has reports whether id was registered.
func (g *Graph) Has(id string) bool {
	_, ok := g.succ[id]
	return ok
}

// Successors returns the nodes directly depending on id.
func (g *Graph) Successors(id string) []string {
	out := make([]string, len(g.succ[id]))
	copy(out, g.succ[id])
	return out
}

// Predecessors returns the direct dependencies of id.
func (g *Graph) Predecessors(id string) []string {
	out := make([]string, len(g.pred[id]))
	copy(out, g.pred[id])
	return out
}
