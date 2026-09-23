// Package fail owns task terminal states, failure propagation and the root
// cause table.
package fail

import (
	"context"
	"errors"
)

// Status is the phase/terminal state of a task.
type Status int

const (
	Waiting Status = iota // not yet eligible
	Ready                 // eligible, never started
	Running               // function has been invoked
	Succeeded
	Failed
	Skipped  // never started
	Canceled // started, aborted by context after a failure
)

// Terminal reports whether the status is final.
func (s Status) Terminal() bool { return s >= Succeeded }

func (s Status) String() string {
	switch s {
	case Waiting:
		return "waiting"
	case Ready:
		return "ready"
	case Running:
		return "running"
	case Succeeded:
		return "succeeded"
	case Failed:
		return "failed"
	case Skipped:
		return "skipped"
	case Canceled:
		return "canceled"
	default:
	return "unknown"
	}
}

// Sentinel errors; concrete states stay distinguishable via Status too.
var (
	ErrPanic    = errors.New("task panicked")
	ErrCanceled = context.Canceled
)

// PanicError wraps a recovered panic value. errors.Is(err, ErrPanic) holds.
type PanicError struct{ Value any }

func (e *PanicError) Error() string { return "task panicked: " + panicText(e.Value) }
func (e *PanicError) Is(t error) bool { return t == ErrPanic }

func panicText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case error:
		return x.Error()
	default:
		return toString(v)
	}
}

// Cause identifies the root failure a terminal state is attributed to.
type Cause struct {
	Rank int    // failure ordinal, lower = earlier
	ID   string // tie breaker and human-readable origin
}

// TaskState is the immutable-on-terminal snapshot of one task.
type TaskState struct {
	Status Status
	Err    error
	Cause  *Cause
}

// Tracker is a single-writer state machine (the scheduler coordinator is the
// only writer; readers may call Snapshot under RLock).
type Tracker struct {
	states map[string]*TaskState
	next   int
}

// NewTracker returns a tracker with every id in Waiting.
func NewTracker(ids []string) *Tracker {
	t := &Tracker{states: make(map[string]*TaskState, len(ids))}
	for _, id := range ids {
		t.states[id] = &TaskState{Status: Waiting}
	}
	return t
}

func (t *Tracker) get(id string) *TaskState {
	s := t.states[id]
	if s == nil {
		s = &TaskState{Status: Waiting}
		t.states[id] = s
	}
	return s
}

// Begin moves a not-started task to Running. Returns false if it already
// started or is terminal.
func (t *Tracker) Begin(id string) bool {
	s := t.get(id)
	if s.Status != Waiting && s.Status != Ready {
		return false
	}
	s.Status = Running
	return true
}

// Succeed marks a Running task Succeeded; a terminal task is untouched.
func (t *Tracker) Succeed(id string) {
	s := t.get(id)
	if s.Terminal() {
		return
	}
	s.Status = Succeeded
}

// Fail marks a Running task Failed, assigning the next failure rank. A
// terminal task (e.g. already Canceled) is left untouched.
func (t *Tracker) Fail(id string, err error) (Cause, bool) {
	s := t.get(id)
	if s.Terminal() {
		if s.Cause != nil {
			return *s.Cause, false
		}
		return Cause{}, false
	}
	c := Cause{Rank: t.next, ID: id}
	t.next++
	s.Status, s.Err, s.Cause = Failed, err, &c
	return c, true
}

// Cancel marks a Running task Canceled due to root c. Terminal tasks and
// never-started tasks are left to Skip. Late writes lose because terminal
// states are never overwritten.
func (t *Tracker) Cancel(id string, c Cause) {
	s := t.get(id)
	if s.Terminal() || s.Status != Running {
		return
	}
	s.Status, s.Err, s.Cause = Canceled, ErrCanceled, &c
}

// Skip marks a never-started task Skipped, attributed to root c.
func (t *Tracker) Skip(id string, c Cause) {
	s := t.get(id)
	if s.Terminal() || s.Status == Running {
		return
	}
	s.Status, s.Err, s.Cause = Skipped, ErrCanceled, &c
}

// SetReady flips Waiting -> Ready (bookkeeping, non-terminal).
func (t *Tracker) SetReady(id string) {
	if s := t.get(id); s.Status == Waiting {
		s.Status = Ready
	}
}

// State returns a copy of one task's state.
func (t *Tracker) State(id string) TaskState { return *t.get(id) }
