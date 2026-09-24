// Package sched selects the next task by weighted-fair virtual time via a heap.
package sched

import (
	"context"
	"errors"
	"sync"
	"time"

	"ontology/task"
	"ontology/tenant"
)

var (
	// ErrExists is returned when adding a duplicate tenant id.
	ErrExists = errors.New("sched: tenant already exists")
	// ErrUnknownTenant is returned when acting on a missing/removed tenant.
	ErrUnknownTenant = errors.New("sched: unknown tenant")
)

// Scheduler is a thread-safe weighted-fair scheduler with an injectable clock.
type Scheduler struct {
	mu    sync.Mutex
	cond  *sync.Cond
	clock task.Clock
	seq   int64
	ten   map[string]*tenant.Tenant
	heap  []*tenant.Tenant
	cmp   int // comparisons during the in-progress/last selection
}

// New builds a scheduler; nil clock yields a zero-time deterministic clock.
func New(clock task.Clock) *Scheduler {
	s := &Scheduler{ten: make(map[string]*tenant.Tenant), clock: clock}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Add registers a weighted tenant.
func (s *Scheduler) Add(id string, weight float64) error {
	t, err := tenant.New(id, weight)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ten[id]; ok {
		return ErrExists
	}
	s.ten[id] = t
	return nil
}

// Remove unregisters a tenant and returns its still-queued tasks.
func (s *Scheduler) Remove(id string) ([]task.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.ten[id]
	if !ok {
		return nil, ErrUnknownTenant
	}
	delete(s.ten, id)
	if t.Len() > 0 {
		s.heapRemove(t.Index())
	}
	return t.Drain(), nil
}

// HeapLen returns the number of non-empty tenants currently in the heap.
func (s *Scheduler) HeapLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.heap)
}

// LastComparisons returns comparison count recorded for the last selection.
func (s *Scheduler) LastComparisons() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmp
}

// Submit appends a task to a registered tenant queue.
func (s *Scheduler) Submit(ctx context.Context, id string, cost float64) (task.Task, error) {
	s.mu.Lock()
	t, ok := s.ten[id]
	if !ok {
		s.mu.Unlock()
		return task.Task{}, ErrUnknownTenant
	}
	s.seq++
	seq := s.seq
	var now time.Time
	if s.clock != nil {
		now = s.clock.Now()
	}
	j, err := task.New(id, cost, seq, now)
	if err != nil {
		s.seq--
		s.mu.Unlock()
		return task.Task{}, err
	}
	wasEmpty := t.WasEmpty()
	t.Push(j)
	if wasEmpty {
		t.Rejoin(s.systemVTLocked())
		s.heapPush(t)
	}
	s.cond.Signal()
	s.mu.Unlock()
	return j, nil
}

// Next blocks until a task is available or ctx is done.
func (s *Scheduler) Next(ctx context.Context) (task.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stop := context.AfterFunc(ctx, func() {
		s.mu.Lock()
		s.cond.Broadcast()
		s.mu.Unlock()
	})
	defer stop()
	for len(s.heap) == 0 {
		if err := ctx.Err(); err != nil {
			return task.Task{}, err
		}
		s.cond.Wait()
	}
	return s.popLocked(), nil
}

// TryNext returns a task immediately or false if all queues are empty.
func (s *Scheduler) TryNext() (task.Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.heap) == 0 {
		return task.Task{}, false
	}
	return s.popLocked(), true
}

func (s *Scheduler) popLocked() task.Task {
	s.cmp = 0
	root := s.heap[0]
	j := root.Pop()
	root.Advance(j.Cost)
	if root.Len() == 0 {
		s.heapRemove(0)
	} else {
		// root vt rose: sift it down to restore heap order.
		s.siftDown(0)
	}
	return j
}

func (s *Scheduler) systemVTLocked() float64 {
	if len(s.heap) == 0 {
		return 0
	}
	return s.heap[0].VT()
}

func (s *Scheduler) less(a, b *tenant.Tenant) bool {
	s.cmp++
	if a.VT() != b.VT() {
		return a.VT() < b.VT()
	}
	return a.ID < b.ID
}

func (s *Scheduler) swap(i, j int) {
	s.heap[i], s.heap[j] = s.heap[j], s.heap[i]
	s.heap[i].SetIndex(i)
	s.heap[j].SetIndex(j)
}

func (s *Scheduler) heapPush(t *tenant.Tenant) {
	t.SetIndex(len(s.heap))
	s.heap = append(s.heap, t)
	s.siftUp(len(s.heap) - 1)
}

func (s *Scheduler) heapRemove(i int) {
	last := len(s.heap) - 1
	if i != last {
		s.swap(i, last)
		s.heap[last].SetIndex(-1)
		s.heap = s.heap[:last]
		s.siftUp(i)
		s.siftDown(i)
		return
	}
	s.heap[last].SetIndex(-1)
	s.heap = s.heap[:last]
}

func (s *Scheduler) siftUp(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !s.less(s.heap[i], s.heap[p]) {
			return
		}
		s.swap(i, p)
		i = p
	}
}

func (s *Scheduler) siftDown(i int) {
	n := len(s.heap)
	for {
		l, r, best := 2*i+1, 2*i+2, i
		if l < n && s.less(s.heap[l], s.heap[best]) {
			best = l
		}
		if r < n && s.less(s.heap[r], s.heap[best]) {
			best = r
		}
		if best == i {
			return
		}
		s.swap(i, best)
		i = best
	}
}
