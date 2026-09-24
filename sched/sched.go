// Package sched implements weighted-fair selection by virtual time.
//
// The tenant with the smallest virtual time is chosen; ties break by
// lexicographic tenant ID. Active (non-empty) tenants live in a binary
// heap, so one selection costs O(log n) comparisons; idle tenants are
// not in the heap and cost nothing. See DESIGN.md for the derivation.
package sched

import (
	"container/heap"

	"ontology/task"
	"ontology/tenant"
)

// tHeap is a min-heap of active tenants ordered by (vt, ID).
type tHeap struct {
	items []*tenant.Tenant
	cmp   *int
}

func (h *tHeap) Len() int { return len(h.items) }

func (h *tHeap) Less(i, j int) bool {
	*h.cmp++
	a, b := h.items[i], h.items[j]
	if a.VT() != b.VT() {
		return a.VT() < b.VT()
	}
	return a.ID < b.ID
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

// Scheduler picks the next task across tenants by weighted fairness.
// It is not safe for concurrent use; wrap it (see package admit).
type Scheduler struct {
	h       tHeap
	tenants map[string]*tenant.Tenant
	active  map[string]bool
	sysVT   float64
	cmp     int
}

// New creates an empty scheduler.
func New() *Scheduler {
	s := &Scheduler{
		tenants: make(map[string]*tenant.Tenant),
		active:  make(map[string]bool),
	}
	s.h.cmp = &s.cmp
	return s
}

// AddTenant registers a tenant.
func (s *Scheduler) AddTenant(t *tenant.Tenant) { s.tenants[t.ID] = t }

// RemoveTenant drops a tenant. The caller must guarantee its queue is
// empty (package admit enforces this); otherwise the call is a no-op.
func (s *Scheduler) RemoveTenant(id string) {
	t, ok := s.tenants[id]
	if !ok || t.Len() > 0 {
		return
	}
	delete(s.tenants, id)
	delete(s.active, id)
}

// Tenant returns the tenant with the given ID, or nil.
func (s *Scheduler) Tenant(id string) *tenant.Tenant { return s.tenants[id] }

// Submit enqueues a task for its tenant. A tenant transitioning from
// empty to non-empty re-enters the heap with vt lifted to the system
// virtual time, so it cannot monopolize (DESIGN.md section 2).
func (s *Scheduler) Submit(tsk task.Task) {
	t := s.tenants[tsk.Tenant]
	t.Enqueue(tsk)
	if !s.active[t.ID] {
		s.active[t.ID] = true
		t.Lift(s.sysVT)
		heap.Push(&s.h, t)
	}
}

// Next removes and returns the next task to execute, advancing the
// tenant's virtual time. The second result is false when no task is
// queued anywhere.
func (s *Scheduler) Next() (task.Task, bool) {
	s.cmp = 0
	if len(s.h.items) == 0 {
		return task.Task{}, false
	}
	t := heap.Pop(&s.h).(*tenant.Tenant)
	s.sysVT = t.VT()
	tsk := t.Dequeue()
	t.Advance(tsk.Cost)
	if t.Len() > 0 {
		heap.Push(&s.h, t)
	} else {
		delete(s.active, t.ID)
	}
	return tsk, true
}

// LastComparisons reports how many tenant comparisons the most recent
// Next call performed.
func (s *Scheduler) LastComparisons() int { return s.cmp }

// Active reports how many tenants are currently in the heap, i.e. have
// queued tasks. Idle tenants do not count.
func (s *Scheduler) Active() int { return len(s.h.items) }
