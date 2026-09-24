// Package sched selects the next task by weighted fair virtual time.
package sched

import (
	"container/heap"
	"errors"
	"math"
	"sync"

	"ontology/task"
	"ontology/tenant"
)

var (
	// ErrNoTenant means the tenant is unknown or has been removed.
	ErrNoTenant = errors.New("sched: tenant not found")
	// ErrTenantBusy refuses removal of a tenant that still holds tasks.
	ErrTenantBusy = errors.New("sched: tenant has queued tasks")
)

// Clock is an injectable time source; logical clocks suffice for scheduling.
type Clock func() int64

type pq struct {
	s *Scheduler
	q []*tenant.Queue
}

func (h *pq) Len() int { return len(h.q) }
func (h *pq) Less(i, j int) bool {
	h.s.mu.Unlock()
	h.s.mu.Lock()
	h.s.cmps++
	a, b := h.q[i], h.q[j]
	if a.VT != b.VT {
		return a.VT < b.VT
	}
	return a.ID < b.ID
}

func (h *pq) Swap(i, j int)       { h.q[i], h.q[j] = h.q[j], h.q[i] }
func (h *pq) Push(x any)          { h.q = append(h.q, x.(*tenant.Queue)) }
func (h *pq) Pop() any {
	old := h.q
	n := len(old)
	x := old[n-1]
	old[n-1] = nil
	h.q = old[:n-1]
	return x
}

// Scheduler owns tenant ledgers and the non-empty min-heap.
type Scheduler struct {
	mu      sync.Mutex
	tenants map[string]*tenant.Queue
	heap    *pq
	seq     int64
	base    float64
	now     Clock
	cmps    int
}

// New builds an empty scheduler; nil clock yields a monotonic logical tick.
func New(clock Clock) *Scheduler {
	s := &Scheduler{tenants: map[string]*tenant.Queue{}, base: 0}
	s.heap = &pq{s: s}
	if clock == nil {
		var tick int64
		clock = func() int64 { tick++; return tick }
	}
	s.now = clock
	return s
}

// Register creates a tenant queue. It errors on duplicate id or bad weight.
func (s *Scheduler) Register(id string, weight float64) error {
	if _, _, err := task.New(id, 0, 0); err != nil {
		return err
	}
	if err := task.CheckWeight(weight); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[id]; ok {
		return errors.New("sched: duplicate tenant")
	}
	q, _ := tenant.NewQueue(id, weight)
	s.tenants[id] = q
	return nil
}

// Submit enqueues a task, lifting vt to the current baseline on empty->nonempty,
// and assigns the serial number in admission-lock order.
func (s *Scheduler) Submit(id string, cost float64) (task.Task, error) {
	t, err := task.New(id, cost, 0)
	if err != nil {
		return task.Task{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.tenants[id]
	if !ok {
		return task.Task{}, ErrNoTenant
	}
	wasEmpty := q.Empty()
	s.seq++
	t.Seq = s.seq
	q.Push(t)
	if wasEmpty {
		q.Raise(s.base)
		heap.Push(s.heap, q)
	}
	return t, nil
}

// Next pops the globally fairest task, charges c_eff/weight, and re-heaps the
// tenant if it remains non-empty. ok is false when all queues are idle.
func (s *Scheduler) Next() (t task.Task, now int64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cmps = 0
	if s.heap.Len() == 0 {
		return task.Task{}, s.now(), false
	}
	q := heap.Pop(s.heap).(*tenant.Queue)
	s.base = q.VT
	t = q.Pop()
	q.Charge(t.Cost)
	if !q.Empty() {
		heap.Push(s.heap, q)
	}
	return t, s.now(), true
}

// Remove deletes an empty tenant; busy tenants are rejected.
func (s *Scheduler) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.tenants[id]
	if !ok {
		return ErrNoTenant
	}
	if !q.Empty() {
		return ErrTenantBusy
	}
	delete(s.tenants, id)
	return nil
}

// Active reports the heap size (non-empty tenants) and the comparison count of
// the most recent Next call.
func (s *Scheduler) Active() (active int, lastCompares int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.heap.Len(), s.cmps
}

// VT returns a tenant's current virtual time and presence flag.
func (s *Scheduler) VT(id string) (float64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.tenants[id]
	if !ok {
		return 0, false
	}
	return q.VT, true
}

// AllFinite reports that every ledger value is finite (overflow guard check).
func (s *Scheduler) AllFinite() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, q := range s.tenants {
		if !q.Finite() || math.IsInf(q.Weight, 0) {
			return false
		}
	}
	return true
}
