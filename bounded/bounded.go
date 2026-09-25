// Package bounded applies depth and cardinality bounds to a graph traversal
// and reports a mutually exclusive three-state result.
package bounded

import (
	"errors"

	"ontology/graph"
	"ontology/traverse"
)

type Status int

const (
	Complete Status = iota // every reachable node was naturally visited
	DepthCut               // stopped because MaxDepth hid further nodes
	LimitCut               // stopped because Limit left nodes unreturned
)

var (
	ErrDepthCut = errors.New("bounded: traversal cut by max depth")
	ErrLimitCut = errors.New("bounded: traversal cut by limit")
)

// Error maps a cut status to its sentinel error; Complete maps to nil.
func (s Status) Error() error {
	switch s {
	case DepthCut:
		return ErrDepthCut
	case LimitCut:
		return ErrLimitCut
	default:
		return nil
	}
}

func (s Status) String() string {
	switch s {
	case DepthCut:
		return "DepthCut"
	case LimitCut:
		return "LimitCut"
	default:
		return "Complete"
	}
}

// Result is the bounded traversal outcome. Emitted and Unreturned partition the
// in-bounds reachable node set (nodes whose depth is <= MaxDepth), so
// Emitted+Unreturned == Reachable always holds.
type Result struct {
	Nodes      []string
	Status     Status
	Emitted    int
	Unreturned int
	Reachable  int
}

// Run performs a BFS from start under maxDepth (start is depth 0) and limit
// (maximum returned nodes). Nodes are emitted at most once, at first reach.
func Run(g *graph.Graph, start string, dir traverse.Dir, maxDepth, limit int) (*Result, error) {
	if !g.HasNode(start) {
		return nil, traverse.ErrStartNotFound
	}
	if !dir.Valid() {
		return nil, traverse.ErrInvalidDir
	}
	if maxDepth < 0 {
		return nil, errors.New("bounded: maxDepth must be >= 0")
	}
	if limit <= 0 {
		return nil, errors.New("bounded: limit must be > 0")
	}

	// Phase 1: one BFS pass collects every in-bounds node (depth <= maxDepth)
	// in first-reach order. Nodes only enter the discovered set once, so a
	// cycle revisits but never duplicates a node.
	discovered := map[string]bool{start: true}
	order := []string{start}
	current := []string{start}
	for depth := 0; depth < maxDepth && len(current) > 0; depth++ {
		var next []string
		for _, node := range current {
			for _, nb := range traverse.Neighbors(g, node, dir) {
				if discovered[nb] {
					continue
				}
				discovered[nb] = true
				order = append(order, nb)
				next = append(next, nb)
			}
		}
		current = next
	}

	// Phase 2: current holds depth-maxDepth nodes. A neighbor not yet
	// discovered would live at maxDepth+1: evidence of a depth cut. A neighbor
	// already discovered (e.g. a cycle back-edge) is NOT evidence.
	depthPending := false
	if len(current) > 0 {
		for _, node := range current {
			for _, nb := range traverse.Neighbors(g, node, dir) {
				if !discovered[nb] {
					depthPending = true
				}
			}
		}
	}

	reachable := len(order)
	take := limit
	if take > len(order) {
		take = len(order)
	}
	nodes := order[:take]
	unreturned := reachable - len(nodes)

	status := Complete
	switch {
	case unreturned > 0:
		status = LimitCut
	case depthPending:
		status = DepthCut
	}
	return &Result{
		Nodes:      nodes,
		Status:     status,
		Emitted:    len(nodes),
		Unreturned: unreturned,
		Reachable:  reachable,
	}, nil
}
