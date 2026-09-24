// Package walk implements budgeted breadth-first traversal over a graph with
// deterministic truncation and resumable checkpoints.
package walk

import (
	"errors"
	"fmt"

	"ontology/graph"
)

// ErrStartMissing is returned when the start node of an initial checkpoint
// is not present in the graph.
var ErrStartMissing = errors.New("walk: start node not in graph")

// ErrNodeGone is returned when a node referenced by the frontier was removed
// from the graph between segments. The error message names the node.
var ErrNodeGone = errors.New("walk: queued node missing from graph")

// Cursor is a lazy expansion pointer: the next out-edge of Node to examine.
type Cursor struct {
	Node string
	Edge int
}

// Checkpoint is the full resumable BFS state: the visited set (in visit
// order) plus the frontier (ready nodes and expansion cursors).
type Checkpoint struct {
	Ready   []string
	Pending []Cursor
	Visited []string
}

// Initial returns the checkpoint that starts a traversal at start.
func Initial(start string) Checkpoint {
	return Checkpoint{Ready: []string{start}}
}

// Done reports whether the traversal has exhausted the reachable graph.
func (c Checkpoint) Done() bool {
	return len(c.Ready) == 0 && len(c.Pending) == 0
}

// Stats holds traversal counters snapshot from the walker's internal state.
type Stats struct {
	Visited       int // nodes first-visited during this call
	PeakQueue     int // max of len(ready)+len(pending) observed
	EdgesExamined int // out-edges examined by cursors
}

// Result is the outcome of one Walk call.
type Result struct {
	Visited []string // nodes visited during this call, in order
	Next    Checkpoint
	Stats   Stats
}

// walker holds the mutable traversal state for a single Walk call.
type walker struct {
	g       *graph.Graph
	ready   []string
	pending []Cursor
	seen    map[string]bool
	order   []string

	peak  int
	edges int
	err   error
}

// Walk advances the traversal from checkpoint c, visiting at most budget
// nodes. It is pure: g and c are not mutated, and no state is shared between
// calls. Budget counts visited nodes; a zero budget returns c unchanged.
func Walk(g *graph.Graph, c Checkpoint, budget int) (Result, error) {
	w := newWalker(g, c)
	base := len(w.order)
	if err := w.run(budget); err != nil {
		return Result{}, err
	}
	res := Result{
		Visited: append([]string(nil), w.order[base:]...),
		Next: Checkpoint{
			Ready:   append([]string(nil), w.ready...),
			Pending: append([]Cursor(nil), w.pending...),
			Visited: append([]string(nil), w.order...),
		},
	}
	res.Stats = Stats{Visited: len(w.order) - base, PeakQueue: w.peak, EdgesExamined: w.edges}
	return res, nil
}

// newWalker restores the traversal state from a checkpoint.
func newWalker(g *graph.Graph, c Checkpoint) *walker {
	w := &walker{
		g:       g,
		ready:   append([]string(nil), c.Ready...),
		pending: append([]Cursor(nil), c.Pending...),
		seen:    make(map[string]bool, len(c.Visited)),
		order:   append([]string(nil), c.Visited...),
	}
	for _, id := range c.Visited {
		w.seen[id] = true
	}
	return w
}

// run visits at most budget nodes, advancing the frontier lazily.
func (w *walker) run(budget int) error {
	base := len(w.order)
	w.notePeak()
	for len(w.order)-base < budget {
		id, ok := w.next()
		if w.err != nil {
			return w.err
		}
		if !ok {
			break
		}
		if !w.g.Has(id) {
			if len(w.order) == 0 {
				return fmt.Errorf("%w: %q", ErrStartMissing, id)
			}
			return fmt.Errorf("%w: %q", ErrNodeGone, id)
		}
		if w.seen[id] {
			continue
		}
		w.seen[id] = true
		w.order = append(w.order, id)
		if len(w.g.Out(id)) > 0 {
			w.pending = append(w.pending, Cursor{Node: id})
		}
		w.notePeak()
	}
	return nil
}

// next returns the next candidate node to visit, lazily pulling one edge at
// a time from the front cursor so out-edges are never enqueued in bulk.
func (w *walker) next() (string, bool) {
	for {
		if len(w.ready) > 0 {
			id := w.ready[0]
			w.ready = w.ready[1:]
			return id, true
		}
		if len(w.pending) == 0 {
			return "", false
		}
		cur := &w.pending[0]
		if !w.g.Has(cur.Node) {
			w.err = fmt.Errorf("%w: %q", ErrNodeGone, cur.Node)
			return "", false
		}
		edges := w.g.Out(cur.Node)
		if cur.Edge >= len(edges) {
			w.pending = w.pending[1:]
			continue
		}
		cur.Edge++
		w.edges++
		w.ready = append(w.ready, edges[cur.Edge-1])
		w.notePeak()
	}
}

func (w *walker) notePeak() {
	if n := len(w.ready) + len(w.pending); n > w.peak {
		w.peak = n
	}
}
