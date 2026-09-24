// Package eng holds the engine state: the last accepted raw offset, its
// unwrapped value, event counts and an internal comparison counter.
// It depends only on package wrap.
package eng

import (
	"errors"
	"sync"

	"ontology/wrap"
)

// Event re-exports the classification type from wrap.
type Event = wrap.Event

// Re-exported event constants for callers of eng.
const (
	First     = wrap.First
	Forward   = wrap.Forward
	Wrap      = wrap.Wrap
	Duplicate = wrap.Duplicate
)

// Re-exported sentinel errors; ErrEmpty originates at this state layer.
var (
	ErrRegression = wrap.ErrRegression
	ErrOverflow   = wrap.ErrOverflow
	ErrEmpty      = errors.New("eng: no offset has been accepted yet")
)

// Engine is the mutable per-partition state. Construct with New.
type Engine struct {
	mu        sync.Mutex
	threshold uint32
	prev      uint32 // last accepted raw offset
	pu        int64  // last accepted unwrapped offset
	hasPrev   bool
	counts    map[Event]int
	// comparisons is the number of previously accepted offsets inspected by
	// the most recent Feed to reach a decision (incremental: always 1 after
	// the first offset, 0 for the first). Unexported: internal tests read it
	// directly; it is never exposed through any exported method.
	comparisons int
}

// New creates an engine. Threshold validation is the caller's (api) job.
func New(threshold uint32) *Engine {
	return &Engine{threshold: threshold, counts: map[Event]int{}}
}

// Feed accepts one raw offset. A rejected offset (regression or overflow)
// changes nothing: prev, pu and counts stay as they were.
func (e *Engine) Feed(r uint32) (Event, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	// The decision inspects exactly one retained offset (prev): O(1) state.
	if e.hasPrev {
		e.comparisons = 1
	} else {
		e.comparisons = 0
	}
	ev, err := wrap.Classify(e.prev, r, e.hasPrev, e.threshold)
	if err != nil {
		return 0, err // regression: state untouched
	}
	nu, err := wrap.Unwrap(e.pu, e.prev, r, ev)
	if err != nil {
		return 0, err // overflow: state untouched
	}
	// Commit only after both steps succeeded.
	e.prev, e.pu, e.hasPrev = r, nu, true
	e.counts[ev]++
	return ev, nil
}

// LastUnwrapped returns the most recent accepted unwrapped offset.
func (e *Engine) LastUnwrapped() (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.hasPrev {
		return 0, ErrEmpty
	}
	return e.pu, nil
}

// LastRaw returns the most recent accepted raw offset.
func (e *Engine) LastRaw() (uint32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.hasPrev {
		return 0, ErrEmpty
	}
	return e.prev, nil
}

// Counts returns a snapshot copy of the per-event accepted counts.
func (e *Engine) Counts() map[Event]int {
	e.mu.Lock()
	defer e.mu.Unlock()
	snap := make(map[Event]int, len(e.counts))
	for k, v := range e.counts {
		snap[k] = v
	}
	return snap
}
