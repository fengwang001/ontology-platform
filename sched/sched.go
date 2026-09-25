// Package sched implements weighted-fair selection over tenant queues using
// a binary heap ordered by virtual time (ties broken by tenant ID).
package sched

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/task"
	"ontology/tenant"
)

var (
	// ErrNoTenant is returned when submitting to or removing an unknown tenant.
	ErrNoTenant = errors.New("sched: unknown tenant")
	// ErrTenantBusy is returned when removing a tenant that still has tasks.
	ErrTenantBusy = errors.New("sched: tenant has queued tasks")
)

// tHeap is a heap of active tenants ordered by (vt, ID). cmp counts Less
// calls so a single selection's comparison cost can be asserted.
type tHeap struct {
	items []*tenant.Tenant
	cmp   *int
}

func (h *tHeap) Len() int { return len(h.items) }

func (h *tHeap) Less(i, j int) bool {
	*h.cmp++
	a, b := h.items[i], h.items[j]
	if a.VT() == b.VT() {
		return a.ID < b.ID
	}
	return a.VT() < b.VT()
}

func (h *tHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *tHeap) Push(x any) { h.items = append(h.items, x.(*tenant.Tenant)) }

func (h *tHeap) Pop() any {
	old := h.items
	n := len(old)
	it := old[n-1]
	h.items = old[:n-1]
	return it
}

// Scheduler picks the next task by weighted-fair virtual time. Safe for
// concurrent use; submission order is the lock serialization order.
type Scheduler struct {
	mu      sync.Mutex
	tenants map[string]*tenant.Tenant
	h       tHeap
	sysVT   float64
	cmp     int
	lastCmp int
	seq     uint64
	noLift  bool // test-only: disable vt lift to measure its effect
}

// New returns an empty scheduler.
func New() *Scheduler {
	s := &Scheduler{tenants: make(map[string]*tenant.Tenant)}
	s.h.cmp = &s.cmp
	return s
}

// AddTenant registers a tenant; invalid weights are rejected.
func (s *Scheduler) AddTenant(id string, weight float64) error {
	t, err := tenant.New(id, weight)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tenants[id] = t
	return nil
}

// RemoveTenant removes an idle tenant. A tenant with queued tasks is refused
// with ErrTenantBusy so tasks are never silently dropped.
func (s *Scheduler) RemoveTenant(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[id]
	if !ok {
		return ErrNoTenant
	}
	if t.Len() > 0 {
		return ErrTenantBusy
	}
	delete(s.tenants, id)
	return nil
}

// Submit enqueues a task, assigning its sequence number under the lock. A
// tenant transitioning from empty to non-empty is lifted to the system
// virtual time and pushed into the heap.
func (s *Scheduler) Submit(tsk task.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[tsk.Tenant]
	if !ok {
		return ErrNoTenant
	}
	tsk.Seq = s.seq
	s.seq++
	t.Enqueue(tsk)
	if !t.Active() {
		if !s.noLift {
			t.Lift(s.sysVT)
		}
		heap.Push(&s.h, t)
		t.SetActive(true)
	}
	return nil
}

// Next removes and returns the head task of the tenant with the smallest
// virtual time, charging its cost to that tenant's ledger.
func (s *Scheduler) Next() (task.Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.h.Len() == 0 {
		return task.Task{}, false
	}
	s.cmp = 0
	t := s.h.items[0]
	tsk := t.Dequeue()
	if s.sysVT < t.VT() {
		s.sysVT = t.VT()
	}
	t.Advance(tsk.Cost)
	if t.Len() == 0 {
		heap.Pop(&s.h)
		t.SetActive(false)
	} else {
		heap.Fix(&s.h, 0)
	}
	s.lastCmp = s.cmp
	return tsk, true
}

// Compares returns the number of heap comparisons used by the last Next.
func (s *Scheduler) Compares() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastCmp
}

// Active returns the number of non-empty tenants currently in the heap.
func (s *Scheduler) Active() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.h.Len()
}

// QueueLen returns the number of queued tasks for a tenant.
func (s *Scheduler) QueueLen(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tenants[id]; ok {
		return t.Len()
	}
	return 0
}
