// Package sched selects the next task by weighted virtual time using a heap.
package sched

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/task"
	"ontology/tenant"
)

// ErrNoTenant means a submit/remove targeted an unknown tenant id.
var ErrNoTenant = errors.New("sched: unknown tenant")

// ErrQueueNotEmpty means removal was refused while tasks remain queued.
var ErrQueueNotEmpty = errors.New("sched: tenant queue is not empty")

// Scheduler is a thread-safe weighted fair queue over in-memory state.
type Scheduler struct {
	mu       sync.Mutex
	cond     *sync.Cond
	tenants  map[string]*tenant.Queue
	heap     vtHeap
	compares int
	bump     bool
}

// New creates a scheduler that bumps idle tenants to system virtual time.
func New() *Scheduler { return newWithBump(true) }

func newWithBump(bump bool) *Scheduler {
	s := &Scheduler{tenants: map[string]*tenant.Queue{}, bump: bump}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// AddTenant registers a tenant with weight; re-adding a removed id is allowed.
func (s *Scheduler) AddTenant(id string, weight float64) error {
	q, err := tenant.New(id, weight)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tenants[id] = q
	return nil
}

// NumTenants returns the number of registered tenants (empty or not).
func (s *Scheduler) NumTenants() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tenants)
}

// HeapLen returns the number of non-empty tenants currently in the heap.
func (s *Scheduler) HeapLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.heap.Len()
}

// QueueLen returns a tenant's queued task count.
func (s *Scheduler) QueueLen(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if q := s.tenants[id]; q != nil {
		return q.Len()
	}
	return 0
}

// LastCompares returns less() comparisons used by the most recent selection.
func (s *Scheduler) LastCompares() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.compares
}

// Submit enqueues a cost task for id; empty queues are bumped to system vt.
func (s *Scheduler) Submit(id string, cost int64) (task.Task, error) {
	if err := task.ValidateCost(cost); err != nil {
		return task.Task{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.tenants[id]
	if q == nil {
		return task.Task{}, ErrNoTenant
	}
	wasEmpty := q.Len() == 0
	now := 0.0
	if s.heap.Len() > 0 {
		now = s.heap[0].VT()
	}
	if !s.bump {
		now = 0
	}
	t, err := q.Enqueue(cost, now)
	if err != nil {
		return task.Task{}, err
	}
	if wasEmpty {
		q.SetActive(true)
		heap.Push(&s.heap, q)
		s.cond.Broadcast()
	}
	return t, nil
}

// TryNext selects and dispatches one task, or returns zero,false when idle.
func (s *Scheduler) TryNext() (task.Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.popLocked()
}

// Next blocks until a task is available, then dispatches it.
func (s *Scheduler) Next() task.Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.heap.Len() == 0 {
	s.cond.Wait()
	}
	t, _ := s.popLocked()
	return t
}

func (s *Scheduler) popLocked() (task.Task, bool) {
	if s.heap.Len() == 0 {
		return task.Task{}, false
	}
	s.heap.count = &s.compares
	s.compares = 0
	q := heap.Pop(&s.heap).(*tenant.Queue)
	s.heap.count = nil
	t, _ := q.Dequeue()
	q.Advance(t.Cost)
	if q.Len() > 0 {
		heap.Push(&s.heap, q)
} else {
		q.SetActive(false)
	}
	return t, true
}

// RemoveTenant refuses while tasks remain, returning the queued count.
func (s *Scheduler) RemoveTenant(id string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.tenants[id]
	if q == nil {
	return 0, ErrNoTenant
	}
	if q.Len() > 0 {
		return q.Len(), ErrQueueNotEmpty
	}
	delete(s.tenants, id)
	return 0, nil
}
