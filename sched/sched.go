// Package sched implements weighted-fair selection over tenant queues using
// virtual time: the tenant with the smallest virtual time is served next,
// ties break by tenant ID, and idle tenants rejoin at the system virtual time.
package sched

import (
	"container/heap"

	"ontology/task"
	"ontology/tenant"
)

// vtHeap is a min-heap of active (non-empty) tenants ordered by (VT, ID).
// Every Less call increments the shared comparison counter.
type vtHeap struct {
	items []*tenant.Tenant
	cmp   *int
}

func (h vtHeap) Len() int { return len(h.items) }

func (h vtHeap) Less(i, j int) bool {
	*h.cmp++
	a, b := h.items[i], h.items[j]
	if a.VT != b.VT {
		return a.VT < b.VT
	}
	return a.ID < b.ID
}

func (h vtHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *vtHeap) Push(x any) { h.items = append(h.items, x.(*tenant.Tenant)) }

func (h *vtHeap) Pop() any {
	old := h.items
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	h.items = old[:n-1]
	return it
}

// Scheduler picks the next task across tenants. It is not goroutine-safe;
// callers serialise access (see admit.Limiter).
type Scheduler struct {
	tenants map[string]*tenant.Tenant
	active  vtHeap
	sysVT   float64
	cmp     int
}

// New returns an empty scheduler.
func New() *Scheduler {
	s := &Scheduler{tenants: map[string]*tenant.Tenant{}}
	s.active.cmp = &s.cmp
	return s
}

// AddTenant registers a tenant; its virtual time starts at the system
// virtual time so a newcomer gets no backlog advantage.
func (s *Scheduler) AddTenant(id string, weight float64) {
	s.tenants[id] = tenant.New(id, weight, s.sysVT)
}

// RemoveTenant drops an idle tenant. It reports false if the tenant is
// unknown or still has queued tasks.
func (s *Scheduler) RemoveTenant(id string) bool {
	t, ok := s.tenants[id]
	if !ok || t.Len() > 0 {
		return false
	}
	delete(s.tenants, id)
	return true
}

// Tenant returns the named tenant, or nil.
func (s *Scheduler) Tenant(id string) *tenant.Tenant { return s.tenants[id] }

// Submit enqueues a task. A tenant transitioning from empty to non-empty is
// caught up to the system virtual time before entering the heap.
func (s *Scheduler) Submit(tk task.Task) {
	t := s.tenants[tk.Tenant]
	wasEmpty := t.Len() == 0
	t.Push(tk)
	if wasEmpty {
		t.CatchUp(s.sysVT)
		heap.Push(&s.active, t)
	}
}

// Next removes and returns the next task in weighted-fair order. The second
// result is false when no tenant has queued work.
func (s *Scheduler) Next() (task.Task, bool) {
	if s.active.Len() == 0 {
		return task.Task{}, false
	}
	s.cmp = 0
	t := heap.Pop(&s.active).(*tenant.Tenant)
	if t.VT > s.sysVT {
		s.sysVT = t.VT
	}
	tk := t.Pop()
	t.Charge(tk.Cost)
	if t.Len() > 0 {
		heap.Push(&s.active, t)
	}
	return tk, true
}

// LastComparisons is the number of heap comparisons the last Next call made.
func (s *Scheduler) LastComparisons() int { return s.cmp }

// Active is the number of non-empty tenants currently in the heap.
func (s *Scheduler) Active() int { return s.active.Len() }

// NumTenants is the total number of registered tenants.
func (s *Scheduler) NumTenants() int { return len(s.tenants) }

// SysVT is the current system virtual time.
func (s *Scheduler) SysVT() float64 { return s.sysVT }
