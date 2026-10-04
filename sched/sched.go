// Package sched is the run lifecycle orchestrator on top of group and slot.
//
// One coarse-grained RWMutex serializes all state mutations, which directly
// gives the required "concurrent calls are equivalent to some serial order"
// semantics. A per-operation touch epoch counts the distinct run records
// actually read or written (touched), proving that no operation's cost depends
// on the number of groups or the queue length.
package sched

import (
	"container/list"
	"errors"
	"fmt"
	"sync"

	"ontology/group"
	"ontology/slot"
)

// State of a run.
type State int

const (
	Waiting State = iota
	Running
	Cancelling
	Pending
	Succeeded
	Failed
	Cancelled
	Superseded
)

// IsTerminal reports whether the state is one of the four terminal states.
func (s State) IsTerminal() bool { return s >= Succeeded }

func (s State) String() string {
	switch s {
	case Waiting:
		return "Waiting"
	case Running:
		return "Running"
	case Cancelling:
		return "Cancelling"
	case Pending:
		return "Pending"
	case Succeeded:
		return "Succeeded"
	case Failed:
		return "Failed"
	case Cancelled:
		return "Cancelled"
	case Superseded:
		return "Superseded"
	default:
		return "Unknown"
	}
}

// Sentinel errors; callers distinguish rejection classes with errors.Is.
var (
	// ErrInvalidArgument: group name too long, non-positive id, C/Q out of range.
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrNotFound: the run id does not exist.
	ErrNotFound = errors.New("run not found")
	// ErrConflict: state does not permit the requested operation.
	ErrConflict = errors.New("state conflict")
	// ErrQueueFull: Submit rejected because the queue would grow past Q.
	ErrQueueFull = errors.New("queue full")
)

const maxGroupNameLen = 64

// run is one run record.
type run struct {
	id        int64
	state     State
	group     string
	queueElem *list.Element // non-nil while the run is a Waiting placeholder.
	seen      uint64        // last touchEpoch that touched this record
}

// Scheduler serializes run submission and lifecycle operations.
type Scheduler struct {
	mu         sync.RWMutex
	slots      *slot.Manager
	groups     *group.Table
	runs       map[int64]*run
	nextID     int64
	queueLimit int

	// touchEpoch increments once per mutating/querying operation; r.seen is
	// used to count distinct records touched by that operation.
	touchEpoch uint64
	touched    int
}

// New creates a scheduler with C global execution slots (1..10000) and a
// waiting-queue limit Q (0..100000).
func New(C, Q int) (*Scheduler, error) {
	if C < 1 || C > 10000 || Q < 0 || Q > 100000 {
		return nil, fmt.Errorf("New(C=%d,Q=%d): %w", C, Q, ErrInvalidArgument)
	}
	return &Scheduler{
		slots:      slot.New(C),
		groups:     group.NewTable(),
		runs:       make(map[int64]*run),
		nextID:     1,
		queueLimit: Q,
	}, nil
}

// beginTouch starts a new touched-record accounting period. Callers hold mu.
func (s *Scheduler) beginTouch() {
	s.touchEpoch++
	s.touched = 0
}

// touch marks the run record of id as read/written by the current operation.
func (s *Scheduler) touch(id int64) *run {
	r := s.runs[id]
	if r != nil && r.seen != s.touchEpoch {
		r.seen = s.touchEpoch
		s.touched++
	}
	return r
}

// validateName is a lock-free argument check done before taking the lock.
func validateName(g []byte) bool { return len(g) <= maxGroupNameLen }

// allocate repeatedly moves the queue head to Running while a free slot
// exists and the queue is non-empty. Runs at most one successful pop per
// allocated run record, hence touches O(1) per popped run.
func (s *Scheduler) allocate() {
	for s.slots.CanPop() {
		id := s.slots.Pop()
		r := s.touch(id)
		r.state = Running
	}
}

// vacatePlaceholder handles a placeholder reaching a terminal state. heldSlot
// reports whether the placeholder occupied an execution slot before the
// transition (its state has already been changed to a terminal one). It frees
// that slot, promotes the group's pending run to placeholder at the queue tail,
// and only then lets the caller run the allocation step.
func (s *Scheduler) vacatePlaceholder(r *run, heldSlot bool) {
	if heldSlot {
		s.slots.Release()
	}
	gr := s.groups.Lookup(r.group)
	if gr.Placeholder == r.id {
		gr.Placeholder = 0
		if promoted := gr.Pending; promoted != 0 {
			gr.Pending = 0
			pr := s.touch(promoted)
			pr.state = Waiting
			gr.Placeholder = promoted
			pr.queueElem = s.slots.Enqueue(promoted)
		}
		s.groups.ClearIfEmpty(r.group)
	}
}

// Submit adds a new run. group is a byte string up to 64 bytes; the empty
// group means the run belongs to no group. cancelInProgress asks the
// placeholder to be cancelled; protected protects a Running placeholder from
// that automatic cancellation (but not a Waiting one). It returns the new run
// id, assigned only on acceptance.
func (s *Scheduler) Submit(g []byte, cancelInProgress, protected bool) (int64, error) {
	if !validateName(g) {
		return 0, fmt.Errorf("Submit group len %d: %w", len(g), ErrInvalidArgument)
	}
	name := string(g)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.beginTouch()

	placeholderID := int64(0)
	var gr *group.State
	if name != "" {
		gr = s.groups.Lookup(name)
		placeholderID = gr.Placeholder
	}

	// Submission with no existing placeholder: the new run becomes the
	// placeholder and enters the queue tail. Apply the queue net-growth test
	// before mutating anything.
	if placeholderID == 0 {
		if s.wouldExceedQueueLimit() {
			return 0, fmt.Errorf("Submit: %w", ErrQueueFull)
		}
		id := s.nextID
		s.nextID++
		r := &run{id: id, state: Waiting, group: name, seen: s.touchEpoch}
		s.touched++
		s.runs[id] = r
		if name != "" {
			gr = s.groups.Ensure(name)
			gr.Placeholder = id
		}
		r.queueElem = s.slots.Enqueue(id)
		s.touch(id) // account: enqueued record is read during allocation
		s.allocate()
		return id, nil
	}

	// An existing placeholder is present, so the queue cannot reject this
	// Submit (a pending run never occupies the queue).
	ph := s.touch(placeholderID)
	if old := gr.Pending; old != 0 {
		oldRun := s.touch(old)
		oldRun.state = Superseded
		gr.Pending = 0
	}

	id := s.nextID
	s.nextID++
	r := &run{id: id, state: Pending, group: name, seen: s.touchEpoch}
	s.touched++
	s.runs[id] = r

	if !cancelInProgress {
		gr.Pending = id
		return id, nil
	}

	switch ph.state {
	case Waiting:
		// protected does not protect Waiting placeholders.
		s.slots.Remove(ph.queueElem)
		ph.queueElem = nil
		ph.state = Cancelled
		gr.Placeholder = id
		r.state = Waiting
		r.queueElem = s.slots.Enqueue(id)
		// Net queue change is zero: old placeholder left, new one entered.
		s.touch(id)
		s.allocate()
	case Running:
		if !protected {
			ph.state = Cancelling
		}
		gr.Pending = id
	case Cancelling:
		gr.Pending = id
	default:
		gr.Pending = id
	}
	return id, nil
}

// wouldExceedQueueLimit simulates this Submit plus the trailing allocation
// without mutating state. One new placeholder is appended (L -> L+1, giving
// L1 > L0), then the allocation step pops min(L+1, free) heads.
//
// If any slot is free, at least one pop happens; even when free < L+1 the
// resulting length L+1-free is at most L <= Q, so the test L1 > Q fails. If no
// slot is free, nothing pops and L1 = L+1 > Q exactly when L already equals Q.
// Hence rejection iff every slot is held and the queue is already at Q.
func (s *Scheduler) wouldExceedQueueLimit() bool {
	return s.slots.Free() == 0 && s.slots.Queued() >= s.queueLimit
}

// Finish ends a Running run as Succeeded (ok=true) or Failed (ok=false).
// A Finish arriving for a Cancelling run is accepted but always records
// Cancelled (cancellation wins, ok is discarded).
func (s *Scheduler) Finish(id int64, ok bool) error {
	if id <= 0 {
		return fmt.Errorf("Finish id=%d: %w", id, ErrInvalidArgument)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beginTouch()

	r := s.touch(id)
	if r == nil {
		return fmt.Errorf("Finish id=%d: %w", id, ErrNotFound)
	}
	switch r.state {
	case Running:
		if ok {
			r.state = Succeeded
		} else {
			r.state = Failed
		}
		s.vacatePlaceholder(r, true)
		s.allocate()
		return nil
	case Cancelling:
		r.state = Cancelled
		s.vacatePlaceholder(r, true)
		s.allocate()
		return nil
	default:
		return fmt.Errorf("Finish id=%d in %s: %w", id, r.state, ErrConflict)
	}
}

// AckCancel confirms cancellation of a Cancelling run, moving it to Cancelled
// and releasing its slot.
func (s *Scheduler) AckCancel(id int64) error {
	if id <= 0 {
		return fmt.Errorf("AckCancel id=%d: %w", id, ErrInvalidArgument)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beginTouch()

	r := s.touch(id)
	if r == nil {
		return fmt.Errorf("AckCancel id=%d: %w", id, ErrNotFound)
	}
	if r.state != Cancelling {
		return fmt.Errorf("AckCancel id=%d in %s: %w", id, r.state, ErrConflict)
	}
	r.state = Cancelled
	s.vacatePlaceholder(r, true)
	s.allocate()
	return nil
}

// Cancel requests cancellation: Pending or Waiting runs end immediately as
// Cancelled; a Running run becomes Cancelling (protected never blocks explicit
// user cancellation); cancelling a Cancelling run is a conflict.
func (s *Scheduler) Cancel(id int64) error {
	if id <= 0 {
		return fmt.Errorf("Cancel id=%d: %w", id, ErrInvalidArgument)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beginTouch()

	r := s.touch(id)
	if r == nil {
		return fmt.Errorf("Cancel id=%d: %w", id, ErrNotFound)
	}
	switch r.state {
	case Pending:
		r.state = Cancelled
		if gr := s.groups.Lookup(r.group); gr.Pending == id {
			gr.Pending = 0
			s.groups.ClearIfEmpty(r.group)
		}
		return nil
	case Waiting:
		s.slots.Remove(r.queueElem)
		r.queueElem = nil
		r.state = Cancelled
		s.vacatePlaceholder(r, false)
		s.allocate()
		return nil
	case Running:
		r.state = Cancelling
		return nil
	case Cancelling:
		return fmt.Errorf("Cancel id=%d in %s: %w", id, r.state, ErrConflict)
	default:
		return fmt.Errorf("Cancel id=%d in %s: %w", id, r.state, ErrConflict)
	}
}

// StateOf reports the current state of run id; ErrNotFound when unknown.
func (s *Scheduler) StateOf(id int64) (State, error) {
	if id <= 0 {
		return 0, fmt.Errorf("StateOf id=%d: %w", id, ErrInvalidArgument)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.runs[id]
	if !ok {
		return 0, fmt.Errorf("StateOf id=%d: %w", id, ErrNotFound)
	}
	return r.state, nil
}
