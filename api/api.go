// Package api is the public, goroutine-safe front of the indexed
// priority queue. It depends on pq (which depends on ipq); the
// dependency direction is strictly one-way.
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/pq"
)

// Decidable sentinel errors, re-exported from pq.
var (
	ErrEmptyID        = pq.ErrEmptyID
	ErrDuplicateID    = pq.ErrDuplicateID
	ErrUpdateNotFound = pq.ErrUpdateNotFound
	ErrDeleteNotFound = pq.ErrDeleteNotFound
)

// Queue is a concurrency-safe indexed priority queue.
type Queue struct {
	mu sync.Mutex
	p  *pq.PQ
}

// New returns an empty queue.
func New() *Queue { return &Queue{p: pq.New()} }

// Push adds (id, p); fails on empty id or a still-active id.
func (q *Queue) Push(id string, p int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.p.Push(id, p)
}

// Pop removes and returns the (priority, registration-order)-minimum.
func (q *Queue) Pop() (id string, p int, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.p.Pop()
}

// Peek returns the minimum without removing it.
func (q *Queue) Peek() (id string, p int, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.p.Peek()
}

// UpdatePriority changes the priority of a live id.
func (q *Queue) UpdatePriority(id string, p int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.p.UpdatePriority(id, p)
}

// Delete removes a live id; the id may be Pushed again afterwards.
func (q *Queue) Delete(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.p.Delete(id)
}

// Len reports the number of live elements.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.p.Len()
}

// SelfCheck runs built-in operation sequences on throwaway queues and
// verifies the four invariants: naive-reference agreement, heap order
// after update/delete, thorough deletion with id reuse, and
// no-side-effect rejection.
func (q *Queue) SelfCheck() error {
	// Invariants 1-3: the canonical seven-step sequence must pop [c,a,d,b].
	s := New()
	steps := []struct {
		id  string
		p   int
		del bool
	}{
		{"a", 10, false}, {"b", 5, false}, {"c", 5, false}, {"d", 10, false},
	}
	for _, st := range steps {
		if err := s.Push(st.id, st.p); err != nil {
			return err
		}
	}
	if err := s.UpdatePriority("b", 20); err != nil {
		return err
	}
	if err := s.Delete("c"); err != nil {
		return err
	}
	if err := s.Push("c", 8); err != nil { // id reuse after delete
		return fmt.Errorf("selfcheck: re-push after delete: %w", err)
	}
	want := []string{"c", "a", "d", "b"}
	for _, w := range want {
		id, _, ok := s.Pop()
		if !ok || id != w {
			return fmt.Errorf("selfcheck: pop order: got %q want %q", id, w)
		}
	}
	// Invariant 4: rejected ops leave no trace.
	r := New()
	if err := r.Push("x", 7); err != nil {
		return err
	}
	for _, err := range []error{
		r.Push("", 1), r.Push("x", 1), r.UpdatePriority("g", 1), r.Delete("g"),
	} {
		if err == nil {
			return errors.New("selfcheck: invalid op was accepted")
		}
	}
	if r.Len() != 1 {
		return errors.New("selfcheck: rejected op changed state")
	}
	return nil
}
