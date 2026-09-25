// Package sched implements weighted-fair selection over tenant queues
// via virtual time and a binary min-heap.
package sched

import (
	"container/heap"
	"errors"
	"sync"
	"time"

	"ontology/task"
	"ontology/tenant"
)

var (
	// ErrUnknown is returned when referencing a tenant that was never added.
	ErrUnknown = errors.New("sched: unknown tenant")
	// ErrDuplicate is returned when adding a tenant ID that already exists.
	ErrDuplicate = errors.New("sched: tenant already exists")
	// ErrBusy is returned when removing a tenant that still has queued tasks.
	ErrBusy = errors.New("sched: tenant has queued tasks")
)

// tHeap is a min-heap of tenants ordered by (VT, ID). All comparisons
// are counted so tests can assert the logarithmic bound.
type tHeap struct {
	items    []*tenant.Tenant
	compares *int
}

func (h tHeap) Len() int { return len(h.items) }

func (h tHeap) Less(i, j int) bool {
	*h.compares++
	a, b := h.items[i], h.items[j]
	if a.VT() != b.VT() {
		return a.VT() < b.VT()
	}
	return a.ID() < b.ID()
}

func (h tHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *tHeap) Push(x any)   { h.items = append(h.items, x.(*tenant.Tenant)) }
func (h *tHeap) Pop() any {
	old := h.items
	n := len(old)
	it := old[n-1]
	h.items = old[:n-1]
	return it
}

// Scheduler picks the next task by weighted-fair virtual time.
// It is safe for concurrent use.
type Scheduler struct {
	mu       sync.Mutex
	tenants  map[string]*tenant.Tenant
	h        tHeap
	compares int
	sysVT    float64
	minCost  float64
	lift     bool
	now      func() time.Time
	last     time.Time
}

// Option customizes a Scheduler.
type Option func(*Scheduler)

// WithClock injects a clock used only to timestamp decisions.
func WithClock(now func() time.Time) Option {
	return func(s *Scheduler) { s.now = now }
}

// WithMinCost sets the per-task cost lower bound (default 1) so that
// zero-cost tasks cannot starve others.
func WithMinCost(c float64) Option {
	return func(s *Scheduler) { s.minCost = max(c, 1e-9) }
}

// WithRejoinLift toggles lifting a rejoining tenant's VT to the system
// virtual time (default on); off exists to demonstrate the difference.
func WithRejoinLift(on bool) Option {
	return func(s *Scheduler) { s.lift = on }
}

// New creates an empty Scheduler.
func New(opts ...Option) *Scheduler {
	s := &Scheduler{
		tenants: make(map[string]*tenant.Tenant),
		minCost: 1,
		lift:    true,
		now:     time.Now,
	}
	for _, o := range opts {
		o(s)
	}
	s.h.compares = &s.compares
	return s
}

// Add registers a tenant.
func (s *Scheduler) Add(t *tenant.Tenant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[t.ID()]; ok {
		return ErrDuplicate
	}
	s.tenants[t.ID()] = t
	return nil
}

// Remove unregisters a tenant; queued tasks refuse it with ErrBusy.
func (s *Scheduler) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[id]
	if !ok {
		return ErrUnknown
	}
	if t.Len() > 0 {
		return ErrBusy
	}
	delete(s.tenants, id)
	return nil
}

// Submit enqueues a task; a tenant going empty→non-empty re-enters the
// heap with VT lifted to the system VT.
func (s *Scheduler) Submit(tsk task.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[tsk.Tenant]
	if !ok {
		return ErrUnknown
	}
	if t.Len() == 0 {
		if s.lift {
			t.LiftTo(s.sysVT)
		}
		heap.Push(&s.h, t)
	}
	t.Push(tsk)
	return nil
}

// Next selects and dequeues the next task, or false when none is queued.
func (s *Scheduler) Next() (task.Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compares = 0
	if s.h.Len() == 0 {
		return task.Task{}, false
	}
	t := heap.Pop(&s.h).(*tenant.Tenant)
	tsk := t.Pop()
	if t.VT() > s.sysVT {
		s.sysVT = t.VT()
	}
	cost := tsk.Cost
	if cost < s.minCost {
		cost = s.minCost
	}
	t.Advance(cost)
	if t.Len() > 0 {
		heap.Push(&s.h, t)
	}
	s.last = s.now()
	return tsk, true
}

// Compares returns the heap comparison count of the most recent Next.
func (s *Scheduler) Compares() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.compares
}

// Active returns the heap size, i.e. the number of non-empty tenants.
func (s *Scheduler) Active() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.h.Len()
}

// QueueLen returns the number of queued tasks of a tenant.
func (s *Scheduler) QueueLen(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tenants[id]; ok {
		return t.Len()
	}
	return 0
}

// LastDecision returns the injected-clock timestamp of the last Next.
func (s *Scheduler) LastDecision() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}
