// Package sched is the preemptive priority scheduling state machine:
// current batch, LIFO suspend stack, and the done log. It depends on q.
package sched

import (
	"errors"

	"ontology/q"
)

// Sentinel errors; every rejection leaves the state untouched.
var (
	ErrBadPrio = errors.New("sched: negative priority")
	ErrDupID   = errors.New("sched: duplicate id")
	ErrIdle    = errors.New("sched: no event to process")
)

// Frame is a batch cursor: pos is the next index to process in
// the priority queue prio.
type Frame struct{ Prio, Pos int }

// State is a deep-copied snapshot of the whole machine.
type State struct {
	Pending map[int][]int
	Current *Frame
	Suspend []Frame
	Done    []int
}

// Sched holds all scheduling state in process memory.
type Sched struct {
	qs      *q.Queues
	current *Frame
	suspend []Frame // LIFO: most recently preempted batch on top
	done    []int
	seen    map[int]struct{}

	lastChecked int // buckets examined by the latest Process pick
}

// New returns an idle scheduler.
func New() *Sched {
	return &Sched{qs: q.New(), seen: make(map[int]struct{})}
}

// Submit enqueues id at prio, preempting the current batch when prio
// is strictly higher: the current frame is pushed onto suspend and a
// fresh batch starts at {prio, 0}.
func (s *Sched) Submit(id, prio int) error {
	if prio < 0 {
		return ErrBadPrio
	}
	if _, ok := s.seen[id]; ok {
		return ErrDupID
	}
	s.seen[id] = struct{}{}
	s.qs.Add(prio, id)
	if s.current != nil && prio > s.current.Prio {
		s.suspend = append(s.suspend, *s.current)
		s.current = &Frame{Prio: prio}
	}
	return nil
}

// Process advances one event: it picks the highest non-empty priority
// when idle, consumes pending[current.prio][current.pos], and on batch
// exhaustion resumes the most recently preempted batch (LIFO).
func (s *Sched) Process() (id, prio int, err error) {
	if s.current == nil {
		p, checked, ok := s.qs.Highest()
		s.lastChecked = checked
		if !ok {
			return 0, 0, ErrIdle
		}
		s.current = &Frame{Prio: p}
	}
	prio = s.current.Prio
	id = s.qs.At(prio, s.current.Pos)
	s.done = append(s.done, id)
	s.current.Pos++
	if s.current.Pos >= s.qs.Len(prio) {
		s.qs.Drop(prio)
		if n := len(s.suspend); n > 0 {
			f := s.suspend[n-1]
			s.suspend = s.suspend[:n-1]
			s.current = &f
		} else {
			s.current = nil
		}
	}
	return id, prio, nil
}

// Done returns a copy of the processed IDs in order.
func (s *Sched) Done() []int { return append([]int(nil), s.done...) }

// Snapshot returns a deep copy of the full machine state.
func (s *Sched) Snapshot() State {
	st := State{
		Pending: s.qs.Snapshot(),
		Suspend: append([]Frame(nil), s.suspend...),
		Done:    s.Done(),
	}
	if s.current != nil {
		c := *s.current
		st.Current = &c
	}
	return st
}
