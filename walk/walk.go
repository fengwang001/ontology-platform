// Package walk implements budgeted breadth-first traversal.
//
// The budget unit is the number of visited (first-seen) nodes. Traversal
// is deterministic: out-edges are sorted by target ID (graph package),
// the frontier is a strict FIFO queue, and nodes are marked seen and
// counted at enqueue time. A traversal is a pure function of
// (graph, start, budget): all mutable state lives in a walker value on
// the goroutine's stack, so concurrent resumes of the same token never
// interfere.
package walk

import (
	"errors"
	"fmt"
	"slices"

	"ontology/graph"
	"ontology/resume"
)

// ErrStartMissing reports a walk whose start node is not in the graph.
var ErrStartMissing = errors.New("walk: start node not in graph")

// Stats exposes the traversal counters after a run. During the run the
// counters are unexported fields of the walker and cannot be observed.
type Stats struct {
	Visited       int // nodes first-seen during this segment
	PeakQueue     int // maximum frontier queue length observed
	EdgesExamined int // out-edges inspected during this segment
}

// Result is the outcome of one traversal segment.
type Result struct {
	Seq   []string // nodes visited in this segment, in visit order
	Token []byte   // resume token for the next segment
	Stats Stats
}

// InitialToken returns the resume token of a traversal that has not yet
// visited anything: the queue holds only start and the seen set is empty.
func InitialToken(g *graph.Graph, start string) ([]byte, error) {
	if !g.Has(start) {
		return nil, fmt.Errorf("%w: %q", ErrStartMissing, start)
	}
	return resume.Encode(resume.State{Queue: []string{start}}), nil
}

// Walk runs a fresh traversal from start with the given budget.
func Walk(g *graph.Graph, start string, budget int) (Result, error) {
	tok, err := InitialToken(g, start)
	if err != nil {
		return Result{}, err
	}
	return Resume(g, tok, budget)
}

// Resume continues a traversal from a token produced by an earlier
// segment. A zero (or negative) budget consumes nothing and returns the
// input token unchanged; resuming a finished token yields an empty
// sequence rather than an error.
func Resume(g *graph.Graph, token []byte, budget int) (Result, error) {
	st, err := resume.Decode(g, token)
	if err != nil {
		return Result{}, err
	}
	if budget <= 0 || st.Done {
		return Result{Token: token}, nil
	}
	w := &walker{
		g:      g,
		queue:  slices.Clone(st.Queue),
		cursor: st.FrontCursor,
		seen:   make(map[string]bool, len(st.Seen)),
		peak:   len(st.Queue),
	}
	for _, id := range st.Seen {
		w.seen[id] = true
	}
	seq := w.run(budget)
	return Result{
		Seq:   seq,
		Token: w.token(),
		Stats: Stats{Visited: w.visited, PeakQueue: w.peak, EdgesExamined: w.edges},
	}, nil
}

// walker holds the mutable state of one traversal segment.
type walker struct {
	g       *graph.Graph
	queue   []string // FIFO frontier; only the front frame may have cursor > 0
	cursor  int      // next out-edge index of queue[0]
	seen    map[string]bool
	visited int
	peak    int
	edges   int
}

func (w *walker) run(budget int) []string {
	var seq []string
	for len(seq) < budget && len(w.queue) > 0 {
		front := w.queue[0]
		if !w.seen[front] { // initial frame: visit the start node
			w.visit(front)
			seq = append(seq, front)
			continue
		}
		out := w.g.Out(front)
		if w.cursor >= len(out) {
			w.queue = w.queue[1:]
			w.cursor = 0
			continue
		}
		child := out[w.cursor]
		w.cursor++
		w.edges++
		if w.seen[child] {
			continue
		}
		w.visit(child)
		seq = append(seq, child)
		w.queue = append(w.queue, child)
		if len(w.queue) > w.peak {
			w.peak = len(w.queue)
		}
	}
	return seq
}

func (w *walker) visit(id string) {
	w.seen[id] = true
	w.visited++
}

func (w *walker) token() []byte {
	seen := make([]string, 0, len(w.seen))
	for id := range w.seen {
		seen = append(seen, id)
	}
	slices.Sort(seen)
	return resume.Encode(resume.State{
		Done:        len(w.queue) == 0,
		FrontCursor: w.cursor,
		Queue:       w.queue,
		Seen:        seen,
	})
}
