// Package fail tracks terminal task states and propagates failures.
package fail

import (
	"errors"
	"fmt"

	"ontology/graph"
)

// State is a task's lifecycle state; Success/Failed/Skipped/Canceled are terminal.
type State int

const (
	Pending State = iota
	Success
	Failed
	Skipped
	Canceled
)

func (s State) String() string {
	return [...]string{"pending", "success", "failed", "skipped", "canceled"}[s]
}

// ErrSkipped and ErrCanceled classify non-failure terminal reasons.
var (
	ErrSkipped  = errors.New("fail: skipped")
	ErrCanceled = errors.New("fail: canceled")
)

// SkipError names the root failure that caused a task to be skipped.
type SkipError struct{ Root string }

func (e *SkipError) Error() string {
	return fmt.Sprintf("%s (root cause: %s)", ErrSkipped, e.Root)
}

func (e *SkipError) Is(target error) bool { return target == ErrSkipped }

// Tracker records per-task states; only the scheduler goroutine may call it.
type Tracker struct {
	g      *graph.Graph
	depth  map[string]int
	state  map[string]State
	reason map[string]error
}

// NewTracker builds a tracker from an acyclic graph's topo layers.
func NewTracker(g *graph.Graph, layers [][]string) *Tracker {
	t := &Tracker{g: g, depth: map[string]int{}, state: map[string]State{}, reason: map[string]error{}}
	for d, layer := range layers {
		for _, id := range layer {
			t.depth[id] = d
		}
	}
	for _, id := range g.Tasks() {
		t.state[id] = Pending
	}
	return t
}

func (t *Tracker) State(id string) State { return t.state[id] }

// Reason returns the terminal cause: original error for Failed, ErrCanceled
// for Canceled, and a *SkipError naming the root failure for Skipped.
func (t *Tracker) Reason(id string) error {
	if t.state[id] != Skipped {
		return t.reason[id]
	}
	if r, ok := t.reason[id]; ok {
		return r
	}
	r := &SkipError{Root: t.rootCause(id)}
	t.reason[id] = r
	return r
}

// Succeed marks a finished task successful.
func (t *Tracker) Succeed(id string) { t.state[id] = Success }

// Cancel marks a running task canceled; its late result must be discarded.
func (t *Tracker) Cancel(id string) {
	if t.state[id] == Pending {
		t.state[id] = Canceled
		t.reason[id] = ErrCanceled
	}
}

// Fail records a task failure and skips all of its pending descendants.
func (t *Tracker) Fail(id string, err error) {
	t.state[id] = Failed
	t.reason[id] = err
	seen := map[string]bool{id: true}
	queue := []string{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, ch := range t.g.Children(cur) {
			if seen[ch] {
				continue
			}
			seen[ch] = true
			if t.state[ch] == Pending {
				t.state[ch] = Skipped
			}
			queue = append(queue, ch)
		}
	}
}

// Halt skips every still-pending task with the halt failure as root cause
// (fail-fast stop: not a dependency failure, the scheduler stopped).
func (t *Tracker) Halt(root string) {
	for id, s := range t.state {
		if s == Pending {
			t.state[id] = Skipped
			t.reason[id] = &SkipError{Root: root}
		}
	}
}

// rootCause returns the failed ancestor with the smallest topo depth
// (ties broken by ID), i.e. the earliest real failure upstream of id.
func (t *Tracker) rootCause(id string) string {
	best, bestDepth := "", len(t.depth)+1
	seen := map[string]bool{id: true}
	queue := []string{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, p := range t.g.Parents(cur) {
			if seen[p] {
				continue
			}
			seen[p] = true
			if t.state[p] == Failed && (t.depth[p] < bestDepth || t.depth[p] == bestDepth && p < best) {
				best, bestDepth = p, t.depth[p]
			}
			queue = append(queue, p)
		}
	}
	return best
}
