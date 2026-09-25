// Package sched implements weighted-fair selection over tenant queues.
// Each tenant owns a virtual-time ledger; the scheduler always serves the
// non-empty tenant with the smallest virtual time, breaking ties by tenant
// ID. See DESIGN.md for the derivations.
package sched

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/task"
	"ontology/tenant"
)

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrInvalidWeight  = errors.New("sched: weight must be positive")
	ErrInvalidCost    = errors.New("sched: cost must not be negative")
	ErrUnknownTenant  = errors.New("sched: unknown tenant")
	ErrTenantNotEmpty = errors.New("sched: tenant still has queued tasks")
)

// tHeap is a container/heap of non-empty tenants ordered by (VT, ID).
// cmp counts Less invocations so tests can bound selection complexity.
type tHeap struct {
	items []*tenant.Tenant
	cmp   *int
}

func (h tHeap) Len() int { return len(h.items) }

func (h tHeap) Less(i, j int) bool {
	*h.cmp++
	a, b := h.items[i], h.items[j]
	if a.VT != b.VT {
		return a.VT < b.VT
	}
	return a.ID < b.ID
}

func (h tHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *tHeap) Push(x any) { h.items = append(h.items, x.(*tenant.Tenant)) }

func (h *tHeap) Pop() any {
	old := h.items
	n := len(old)
	it := old[n-1]
	h.items = old[:n-1]
	return it
}

// Scheduler picks the next task to execute across all tenants.
type Scheduler struct {
	mu      sync.Mutex
	tenants map[string]*tenant.Tenant
	h       tHeap
	sysVT   float64 // system virtual time: min VT among recently active tenants
	cmp     int
	lastCmp int
}

// New returns an empty scheduler.
func New() *Scheduler {
	s := &Scheduler{tenants: map[string]*tenant.Tenant{}}
	s.h.cmp = &s.cmp
	return s
}

// AddTenant registers a tenant. Weight must be positive.
func (s *Scheduler) AddTenant(id string, weight float64) error {
	if weight <= 0 {
		return ErrInvalidWeight
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[id]; !ok {
		s.tenants[id] = tenant.New(id, weight)
	}
	return nil
}

// RemoveTenant deletes an idle tenant. A tenant with queued tasks is
// refused with ErrTenantNotEmpty; its tasks stay queued and schedulable.
func (s *Scheduler) RemoveTenant(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[id]
	if !ok {
		return ErrUnknownTenant
	}
	if t.Len() > 0 {
		return ErrTenantNotEmpty
	}
	delete(s.tenants, id)
	return nil
}

// Submit enqueues a task. A tenant going from empty to non-empty has its
// virtual time lifted to the system virtual time so it cannot monopolize
// the scheduler after a long idle period.
func (s *Scheduler) Submit(t task.Task) error {
	if t.Cost < 0 {
		return ErrInvalidCost
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tn, ok := s.tenants[t.Tenant]
	if !ok {
		return ErrUnknownTenant
	}
	if tn.Len() == 0 {
		if tn.VT < s.sysVT {
			tn.VT = s.sysVT
		}
		tn.Enqueue(t)
		heap.Push(&s.h, tn)
	} else {
		tn.Enqueue(t)
	}
	return nil
}

// Next removes and returns the next task to execute, charging its cost to
// the tenant's virtual-time ledger.
func (s *Scheduler) Next() (task.Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cmp = 0
	if len(s.h.items) == 0 {
		s.lastCmp = 0
		return task.Task{}, false
	}
	top := s.h.items[0]
	heap.Pop(&s.h)
	tsk := top.Dequeue()
	if top.VT > s.sysVT {
		s.sysVT = top.VT
	}
	top.Advance(tsk.Cost)
	if top.Len() > 0 {
		heap.Push(&s.h, top)
	}
	s.lastCmp = s.cmp
	return tsk, true
}

// QueueLen reports how many tasks the tenant has queued (0 if unknown).
func (s *Scheduler) QueueLen(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tenants[id]; ok {
		return t.Len()
	}
	return 0
}

// LastComparisons reports the number of heap comparisons the last Next
// call performed.
func (s *Scheduler) LastComparisons() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastCmp
}

// HeapLen reports the number of non-empty tenants taking part in selection.
func (s *Scheduler) HeapLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.h.items)
}

// SysVT reports the current system virtual time.
func (s *Scheduler) SysVT() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sysVT
}
