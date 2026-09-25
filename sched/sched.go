// Package sched holds the state machine behind Schedule/Cancel/Tick: the
// active-id registry, firing history and the monotonic clock. It depends only
// on dlq.
package sched

import (
	"errors"

	"ontology/dlq"
)

// Sentinel errors are all distinct so callers can judge each failure kind.
var (
	ErrEmptyID     = errors.New("sched: task id is empty")
	ErrActiveID    = errors.New("sched: id already has an active task")
	ErrUnknownID   = errors.New("sched: no active task with that id")
	ErrClockRewind = errors.New("sched: tick time went backwards")
)

// S is the task manager. It is not safe for concurrent use on its own; the
// api layer serializes access.
type S struct {
	h        *dlq.Heap
	active   map[string]*dlq.Entry
	lastNow  int64
	haveTick bool
	// history records the ids fired by each Tick, in firing order.
	history [][]string
}

// New returns an empty scheduler.
func New() *S {
	return &S{h: dlq.New(), active: make(map[string]*dlq.Entry)}
}

// Schedule registers id to fire at fireAt. Validation happens before any
// state change, so a rejected call leaves no trace.
func (s *S) Schedule(id string, fireAt int64) error {
	if id == "" {
		return ErrEmptyID
	}
	if _, ok := s.active[id]; ok {
		return ErrActiveID
	}
	s.active[id] = s.h.Schedule(id, fireAt)
	return nil
}

// Cancel lazily cancels the active generation of id. A canceled id is free to
// be scheduled again.
func (s *S) Cancel(id string) error {
	e, ok := s.active[id]
	if !ok {
		return ErrUnknownID
	}
	s.h.Cancel(e)
	delete(s.active, id)
	return nil
}

// Tick advances the clock to now and fires every active task due by now, in
// (fireAt, registration sequence) order. Equal now never refires anything.
func (s *S) Tick(now int64) ([]string, error) {
	if s.haveTick && now < s.lastNow {
		return nil, ErrClockRewind
	}
	fired := s.h.PopDue(now)
	ids := make([]string, 0, len(fired))
	for _, e := range fired {
		delete(s.active, e.ID)
		ids = append(ids, e.ID)
	}
	s.lastNow = now
	s.haveTick = true
	s.history = append(s.history, ids)
	return ids, nil
}

// Peek reports the id and fireAt of the task that would fire next.
func (s *S) Peek() (string, int64, bool) {
	e, ok := s.h.Peek()
	if !ok {
		return "", 0, false
	}
	return e.ID, e.FireAt, true
}

// History returns a copy of the ids fired by every Tick so far.
func (s *S) History() [][]string {
	out := make([][]string, len(s.history))
	for i, ids := range s.history {
		out[i] = append([]string(nil), ids...)
	}
	return out
}
