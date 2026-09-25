// Package sched holds the task-management state machine on top of dlq:
// Schedule/Cancel/Tick, the active-id registry, fire history and monotonic
// clock validation. It depends only on dlq.
package sched

import (
	"errors"

	"ontology/dlq"
)

// Sentinel errors are the decidable failure modes. They are all distinct.
var (
	ErrEmptyID         = errors.New("dlq: empty id")
	ErrDuplicateID     = errors.New("dlq: id already active")
	ErrCancelNotActive = errors.New("dlq: cancel of id that is not active")
	ErrClockRewind     = errors.New("dlq: tick time moved backwards")
)

// Scheduler is the in-memory task state machine. The zero value is NOT
// ready; use New.
type Scheduler struct {
	heap     *dlq.Heap
	active   map[string]*dlq.Handle
	history  []string // ids fired, in fire order
	lastTick int64
	haveTick bool
}

// New returns an empty scheduler.
func New() *Scheduler {
	return &Scheduler{heap: dlq.New(), active: map[string]*dlq.Handle{}}
}

// Schedule registers id to fire at fireAt. It validates before touching any
// state, so a rejected call leaves heap, sequence, history and registry
// untouched.
func (s *Scheduler) Schedule(id string, fireAt int64) error {
	if id == "" {
		return ErrEmptyID
	}
	if _, exists := s.active[id]; exists {
		return ErrDuplicateID
	}
	s.active[id] = s.heap.Push(id, fireAt)
	return nil
}

// Cancel lazily cancels an active id. A missing id is rejected before any
// state change.
func (s *Scheduler) Cancel(id string) error {
	hd, ok := s.active[id]
	if !ok {
		return ErrCancelNotActive
	}
	hd.Cancel()
	delete(s.active, id)
	return nil
}

// Tick advances the clock to now and fires every due active task in
// (fireAt, registration) order. A backwards now is rejected before the heap
// is touched.
func (s *Scheduler) Tick(now int64) ([]string, error) {
	if s.haveTick && now < s.lastTick {
		return nil, ErrClockRewind
	}
	fired := s.heap.PopDue(now)
	for _, id := range fired {
		delete(s.active, id)
	}
	s.history = append(s.history, fired...)
	s.lastTick = now
	s.haveTick = true
	return fired, nil
}

// Peek reports the id/fireAt of the earliest ACTIVE entry without firing it,
// scanning past any cancelled tombstones.
func (s *Scheduler) Peek() (string, int64, bool) {
	var bestID string
	var bestFire int64
	var bestSeq uint64
	found := false
	for id, hd := range s.active {
		if !found || hd.FireAt() < bestFire ||
			(hd.FireAt() == bestFire && hd.Seq() < bestSeq) {
			bestID, bestFire, bestSeq, found = id, hd.FireAt(), hd.Seq(), true
		}
	}
	return bestID, bestFire, found
}

// Fired returns a copy of the accumulated fire history.
func (s *Scheduler) Fired() []string { return append([]string(nil), s.history...) }

// PopCostBounded forwards dlq's heap-pop complexity check as a boolean; the
// raw inspected counter value never crosses this layer.
func PopCostBounded() bool { return dlq.PopCostBounded() }
