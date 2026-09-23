// Package stage provides the generic bounded-queue pipeline stage skeleton:
// bounded capacity, blocking push (back-pressure), in-flight accounting,
// graceful drain and idempotent cancellation.
package stage

import (
	"errors"
	"sync"
	"sync/atomic"
)

// ErrClosed is returned by Push after the stage was cancelled.
var ErrClosed = errors.New("stage: closed")

// Stage is a single-worker bounded queue. Capacity counts the worker slot:
// at most capacity items reside in the stage (capacity-1 buffered + 1 active).
type Stage[T any] struct {
	name string
	ch   chan T
	proc func(T) error

	busy        atomic.Bool
	maxInFlight atomic.Int64
	stopped     atomic.Bool

	mu     sync.Mutex
	closed bool
	done   chan struct{}
	once   sync.Once
	wg     sync.WaitGroup
	err    error
}

// New builds a stage with the given capacity (minimum 1) and processing func.
func New[T any](name string, capacity int, proc func(T) error) *Stage[T] {
	if capacity < 1 {
		capacity = 1
	}
	return &Stage[T]{
		name: name,
		ch:   make(chan T, capacity-1),
		proc: proc,
		done: make(chan struct{}),
	}
}

// Start launches the worker goroutine.
func (s *Stage[T]) Start() {
	s.wg.Add(1)
	go s.loop()
}

func (s *Stage[T]) loop() {
	defer s.wg.Done()
	for {
		select {
		case v, ok := <-s.ch:
			if !ok {
				return
			}
			s.busy.Store(true)
			s.sample()
			err := s.proc(v)
			s.busy.Store(false)
			s.sample()
			if err != nil {
				s.fail(err)
				return
			}
		case <-s.done:
			return
		}
	}
}

func (s *Stage[T]) sample() {
	n := int64(len(s.ch))
	if s.busy.Load() {
		n++
	}
	for {
		old := s.maxInFlight.Load()
		if n <= old || s.maxInFlight.CompareAndSwap(old, n) {
			return
		}
	}
}

// Push blocks until the item is accepted. It returns ErrClosed once cancelled.
func (s *Stage[T]) Push(v T) error {
	if s.stopped.Load() {
		return ErrClosed
	}
	select {
	case s.ch <- v:
		s.sample()
		return nil
	case <-s.done:
		return ErrClosed
	}
}

// Close signals normal completion: queued items are drained, then the worker
// exits. Calling Close before Start is legal. Idempotent.
func (s *Stage[T]) Close() {
	s.once.Do(func() {
		s.stopped.Store(true)
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.ch)
	})
}

// Cancel stops the worker as soon as possible without draining. Idempotent.
func (s *Stage[T]) Cancel() {
	s.once.Do(func() {
		s.stopped.Store(true)
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.done)
	})
}

func (s *Stage[T]) fail(err error) {
	s.stopped.Store(true)
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
	close(s.done)
}

// Wait blocks until the worker has exited and returns its processing error.
func (s *Stage[T]) Wait() error {
	s.wg.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// InFlight reports the current number of items held by this stage.
func (s *Stage[T]) InFlight() int {
	n := len(s.ch)
	if s.busy.Load() {
		n++
	}
	return n
}

// MaxInFlight reports the historical maximum number of held items.
func (s *Stage[T]) MaxInFlight() int64 { return s.maxInFlight.Load() }
