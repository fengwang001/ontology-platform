// Package sched implements weighted-fair selection over tenant queues.
//
// Each tenant carries a virtual time vt; serving a task of effective
// cost c advances vt by c/weight. The runnable tenant with the smallest
// vt (ties broken by tenant ID) is served next, via a binary heap, so a
// single selection costs O(log n) comparisons in the number of
// non-empty tenants. See DESIGN.md for the derivations.
package sched

import (
	"container/heap"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"ontology/task"
	"ontology/tenant"
)

var (
	// ErrInvalidWeight is returned for a zero, negative, or
	// non-finite tenant weight.
	ErrInvalidWeight = errors.New("sched: weight must be positive and finite")
	// ErrEmpty is returned by Next when no tenant has a queued task.
	ErrEmpty = errors.New("sched: no runnable task")
	// ErrTenantBusy is returned when removing a tenant that still
	// has queued tasks; queued tasks are never dropped implicitly.
	ErrTenantBusy = errors.New("sched: tenant has queued tasks")
	// ErrUnknownTenant is returned for submissions to unknown tenants.
	ErrUnknownTenant = errors.New("sched: unknown tenant")
)

// Option configures a Scheduler.
type Option func(*Scheduler)

// WithClock injects the clock used to timestamp served tasks.
func WithClock(now func() time.Time) Option {
	return func(s *Scheduler) { s.now = now }
}

// Scheduler is a multi-tenant weighted-fair queue. It is safe for
// concurrent use; all operations are serialized by one mutex, and the
// submission order under concurrency is the lock acquisition order.
type Scheduler struct {
	mu       sync.Mutex
	now      func() time.Time
	tenants  map[string]*tenant.Tenant
	h        pq
	cmp      int // comparisons of the in-flight selection (non-exported)
	last     int // comparisons of the last completed selection
	servedAt time.Time
}

// New creates an empty scheduler.
func New(opts ...Option) *Scheduler {
	s := &Scheduler{
		now:     time.Now,
		tenants: make(map[string]*tenant.Tenant),
	}
	s.h.cmp = &s.cmp
	for _, o := range opts {
		o(s)
	}
	return s
}

// AddTenant registers a tenant with a positive finite weight.
func (s *Scheduler) AddTenant(id string, weight float64) error {
	if weight <= 0 || math.IsNaN(weight) || math.IsInf(weight, 0) {
		return fmt.Errorf("%w: %q weight %v", ErrInvalidWeight, id, weight)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[id]; ok {
		return fmt.Errorf("sched: tenant %q already exists", id)
	}
	s.tenants[id] = tenant.New(id, weight)
	return nil
}

// RemoveTenant deregisters a tenant. It refuses with ErrTenantBusy
// while tasks remain queued, so queued work is never silently dropped.
func (s *Scheduler) RemoveTenant(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownTenant, id)
	}
	if t.Len() > 0 {
		return fmt.Errorf("%w: %q has %d queued", ErrTenantBusy, id, t.Len())
	}
	delete(s.tenants, id)
	return nil
}

// Submit enqueues a task. A tenant transitioning from empty to
// non-empty is boosted to the current system virtual time (the heap
// root's vt) so a long-idle tenant cannot monopolize the scheduler.
func (s *Scheduler) Submit(tk task.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[tk.Tenant]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownTenant, tk.Tenant)
	}
	if t.Len() == 0 {
		if s.h.Len() > 0 {
			t.Boost(s.h.items[0].VT())
		}
		heap.Push(&s.h, t)
	}
	t.Push(tk)
	return nil
}

// Next selects and dequeues the next task to execute.
func (s *Scheduler) Next() (task.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.h.Len() == 0 {
		return task.Task{}, ErrEmpty
	}
	s.cmp = 0
	t := heap.Pop(&s.h).(*tenant.Tenant)
	tk := t.Pop()
	t.Advance(tk.Effective())
	if t.Len() > 0 {
		heap.Push(&s.h, t)
	}
	s.last = s.cmp
	s.servedAt = s.now()
	return tk, nil
}

// QueueLen reports how many tasks tenant id has queued.
func (s *Scheduler) QueueLen(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tenants[id].Len()
}

// Active reports the heap size, i.e. the number of non-empty tenants.
func (s *Scheduler) Active() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.h.Len()
}

// LastComparisons reports the comparison count of the last selection.
func (s *Scheduler) LastComparisons() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

// LastServedAt reports the injected-clock time of the last Next call.
func (s *Scheduler) LastServedAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.servedAt
}

// pq is a binary heap of non-empty tenants ordered by (vt, ID).
type pq struct {
	items []*tenant.Tenant
	cmp   *int
}

func (p pq) Len() int { return len(p.items) }

func (p pq) Less(i, j int) bool {
	*p.cmp++
	a, b := p.items[i], p.items[j]
	if a.VT() != b.VT() {
		return a.VT() < b.VT()
	}
	return a.ID < b.ID
}

func (p pq) Swap(i, j int) { p.items[i], p.items[j] = p.items[j], p.items[i] }

func (p *pq) Push(x any) { p.items = append(p.items, x.(*tenant.Tenant)) }

func (p *pq) Pop() any {
	old := p.items
	n := len(old)
	it := old[n-1]
	p.items = old[:n-1]
	return it
}
