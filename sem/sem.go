// Package sem is a weighted semaphore with head-of-line blocking: a
// FIFO queue, group wake-up, lock-internal grant commit point.
package sem

import (
	"context"
	"errors"
	"sync"

	"ontology/ledger"
	"ontology/waitq"
)

var (
	// ErrTooLarge rejects a request bigger than the capacity.
	ErrTooLarge = errors.New("sem: request exceeds capacity")
	// ErrTooManyWaiters rejects Acquire when the queue is full.
	ErrTooManyWaiters = errors.New("sem: too many waiters")
	// ErrOverRelease reports a release below zero usage.
	ErrOverRelease = ledger.ErrOverRelease
)

// Stats is a snapshot; WakeChecked/WakeGranted count the waiters
// inspected/granted by the most recent drain.
type Stats struct {
	Used, Cap                int64
	Waiters                  int
	WakeChecked, WakeGranted int
}

type Sem struct {
	mu                     sync.Mutex
	led                    *ledger.Ledger
	q                      *waitq.Queue
	maxWaiters             int
	wakeChecked, wakeGrant int
}

// New returns a Sem with the given capacity and waiter limit.
func New(capacity int64, maxWaiters int) *Sem {
	return &Sem{led: ledger.New(capacity), q: waitq.New(), maxWaiters: maxWaiters}
}

// Acquire blocks until n quota is granted or ctx is done.
func (s *Sem) Acquire(ctx context.Context, n int64) error {
	if n > s.led.Cap() {
		return ErrTooLarge
	}
	s.mu.Lock()
	if s.q.Len() == 0 && s.led.Avail() >= n {
		s.led.Take(n)
		s.mu.Unlock()
		return nil
	}
	if s.q.Len() >= s.maxWaiters {
		s.mu.Unlock()
		return ErrTooManyWaiters
	}
	w := &waitq.Waiter{N: n}
	s.q.Push(w)
	s.mu.Unlock()
	select {
	case <-w.Grant:
		return nil
	case <-ctx.Done():
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if w.Granted { // grant committed under mu; cancellation loses
		return nil
	}
	s.q.Remove(w)
	s.drainLocked() // head may have changed: wake the satisfiable prefix
	return ctx.Err()
}

// TryAcquire takes n without waiting; it never jumps the queue.
func (s *Sem) TryAcquire(n int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n > s.led.Cap() || s.q.Len() > 0 || s.led.Avail() < n {
		return false
	}
	s.led.Take(n)
	return true
}

// Release returns n quota and wakes the satisfiable queue prefix.
func (s *Sem) Release(n int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.led.Give(n); err != nil {
		return err // ledger unchanged
	}
	s.drainLocked()
	return nil
}

func (s *Sem) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{s.led.Used(), s.led.Cap(), s.q.Len(), s.wakeChecked, s.wakeGrant}
}

// drainLocked grants the satisfiable prefix of the queue, in order.
// It checks at most granted+1 waiters.
func (s *Sem) drainLocked() {
	s.wakeChecked, s.wakeGrant = 0, 0
	for w := s.q.Front(); w != nil; w = s.q.Front() {
		s.wakeChecked++
		if s.led.Avail() < w.N {
			return
		}
		s.led.Take(w.N)
		s.q.Pop()
		w.Granted = true
		w.Grant <- struct{}{}
		s.wakeGrant++
	}
}
