// Package sched implements weighted-fair selection over tenant queues
// using virtual time: the tenant with the smallest virtual time is
// served next, ties broken by tenant ID. Selection costs O(log n)
// in the number of non-empty tenants via a binary heap.
package sched

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/task"
	"ontology/tenant"
)

// Errors distinguishable with errors.Is.
var (
	ErrQueueFull     = errors.New("sched: tenant queue full")
	ErrInvalidWeight = errors.New("sched: weight must be positive")
	ErrTenantBusy    = errors.New("sched: tenant has queued tasks")
	ErrUnknownTenant = errors.New("sched: unknown tenant")
)

// tHeap is a min-heap of tenants ordered by (VT, ID). cmp, when set,
// counts comparisons performed during heap operations.
type tHeap struct {
	items []*tenant.Tenant
	cmp   *int
}

func (h tHeap) Len() int { return len(h.items) }

func (h tHeap) Less(i, j int) bool {
	if h.cmp != nil {
		*h.cmp++
	}
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
	old[n-1] = nil
	h.items = old[:n-1]
	return it
}

// Scheduler picks the next task to execute across all tenants.
// It is safe for concurrent use; the submission order under
// concurrency is the serialization order of the internal lock.
type Scheduler struct {
	mu      sync.Mutex
	tenants map[string]*tenant.Tenant
	h       tHeap
	sysVT   float64
	cmp     int
	lastCmp int
}

// New creates an empty scheduler.
func New() *Scheduler {
	s := &Scheduler{tenants: make(map[string]*tenant.Tenant)}
	s.h.cmp = &s.cmp
	return s
}

// Register adds a tenant with the given positive weight.
func (s *Scheduler) Register(id string, weight float64) error {
	if weight <= 0 {
		return ErrInvalidWeight
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tenants[id] = tenant.New(id, weight)
	return nil
}

// Remove deletes a tenant. It refuses while tasks are still queued so
// that submitted work is never silently dropped.
func (s *Scheduler) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[id]
	if !ok {
		return ErrUnknownTenant
	}
	if t.Len() > 0 {
		return ErrTenantBusy
	}
	delete(s.tenants, id)
	return nil
}

// Enqueue appends a task to its tenant's queue, enforcing the
// per-tenant limit. A tenant transitioning from empty to non-empty is
// lifted to the system virtual time so it cannot monopolize the
// scheduler with a stale, far-behind VT.
func (s *Scheduler) Enqueue(tk task.Task, limit int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[tk.Tenant]
	if !ok {
		return ErrUnknownTenant
	}
	if t.Len() >= limit {
		return ErrQueueFull
	}
	wasEmpty := t.Len() == 0
	t.Enqueue(tk)
	if wasEmpty {
		if t.VT < s.sysVT {
			t.VT = s.sysVT
		}
		heap.Push(&s.h, t)
	}
	return nil
}

// Next selects and removes the next task to execute, billing its cost
// to the tenant's virtual time. It reports false when no work is queued.
func (s *Scheduler) Next() (task.Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.h.items) == 0 {
		return task.Task{}, false
	}
	s.cmp = 0
	t := heap.Pop(&s.h).(*tenant.Tenant)
	if t.VT > s.sysVT {
		s.sysVT = t.VT
	}
	tk := t.Dequeue()
	t.Advance(tk.Cost)
	if t.Len() > 0 {
		heap.Push(&s.h, t)
	}
	s.lastCmp = s.cmp
	return tk, true
}

// LastComparisons reports how many heap comparisons the most recent
// Next call performed. It is a test/diagnostic hook proving the
// logarithmic selection cost.
func (s *Scheduler) LastComparisons() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastCmp
}

// Active reports the number of tenants in the heap, i.e. the number of
// tenants with queued tasks. Idle tenants cost nothing at selection.
func (s *Scheduler) Active() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.h.items)
}
