// Package sem 实现带队首阻塞语义的加权信号量。
package sem

import (
	"context"
	"errors"
	"sync"

	"ontology/ledger"
	"ontology/waitq"
)

var (
	ErrTooLarge       = errors.New("sem: request larger than capacity")
	ErrTooManyWaiters = errors.New("sem: too many waiters")
	ErrOverRelease    = ledger.ErrOverRelease
)

type Sem struct {
	mu            sync.Mutex
	book          *ledger.Ledger
	waiters       waitq.Queue
	maxWaiters    int
	releaseChecks int
}

func New(cap, maxWaiters int64) *Sem {
	return &Sem{book: ledger.New(cap), maxWaiters: int(maxWaiters)}
}

type Stats struct {
	Cap, Used, Available, Waiting int64
}

func (s *Sem) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{s.book.Cap(), s.book.Used(), s.book.Available(), int64(s.waiters.Len())}
}

func (s *Sem) Healthy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.book.Healthy()
}

func (s *Sem) ReleaseChecks() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.releaseChecks
}

// Acquire 申请权重 n：不超发、FIFO 不插队、取消零副作用。
func (s *Sem) Acquire(ctx context.Context, n int64) error {
	if n > s.book.Cap() {
		return ErrTooLarge
	}
	s.mu.Lock()
	if s.waiters.Len() == 0 && s.book.CanAllocate(n) {
		s.book.Allocate(n)
		s.mu.Unlock()
		return nil
	}
	if s.waiters.Len() >= s.maxWaiters {
		s.mu.Unlock()
		return ErrTooManyWaiters
	}
	w := waitq.NewWaiter(ctx, n)
	s.waiters.Push(w)
	s.mu.Unlock()
	select {
	case <-w.Ready():
		return nil // 额度已锁内划走；即使 ctx 同时 Done 也改判成功。
	case <-ctx.Done():
		s.mu.Lock()
		if w.Granted {
			s.mu.Unlock()
			return nil
		}
		s.waiters.Remove(w)
		s.drain()
		s.mu.Unlock()
		return ctx.Err()
	}
}

// TryAcquire 非阻塞尝试；队列非空一律 false（不插队）。
func (s *Sem) TryAcquire(n int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n > s.book.Cap() || s.waiters.Len() > 0 || !s.book.CanAllocate(n) {
		return false
	}
	s.book.Allocate(n)
	return true
}

// Release 归还 n 并从队首成组唤醒；非法释放时账本不变。
func (s *Sem) Release(n int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.book.Release(n); err != nil {
		return err
	}
	s.drain()
	return nil
}

// drain 从队首顺序满足，遇到第一个满足不了的即停（调用方持锁）。
func (s *Sem) drain() {
	s.releaseChecks = 0
	for h := s.waiters.Head(); h != nil; h = s.waiters.Head() {
		s.releaseChecks++
		if !s.book.CanAllocate(h.N) {
			return
		}
		s.book.Allocate(h.N)
		s.waiters.Pop().Grant()
	}
}
