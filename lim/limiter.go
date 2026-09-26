// Package lim implements the sliding-window admission decision on top of win.
package lim

import (
	"errors"
	"sync"

	"ontology/win"
)

// Sentinel errors are mutually distinguishable; callers decide via errors.Is.
var (
	ErrNegativeTime = errors.New("lim: timestamp must be non-negative")
	ErrClockRewind  = errors.New("lim: timestamp must be non-decreasing")
)

// Limiter serializes clock-validated Allow calls against one win.Queue.
type Limiter struct {
	limit  int64
	window int64

	mu     sync.Mutex
	lastT  int64
	seen   bool
	q      *win.Queue
	accept int64
}

// New constructs a limiter. Limit and window must both be at least 1;
// otherwise nil and no state is created.
func New(limit, window int64) *Limiter {
	if limit < 1 || window < 1 {
		return nil
	}
	return &Limiter{limit: limit, window: window, q: win.New(window)}
}

// Allow admits at most limit accepted requests per sliding [t-window, t].
// It validates t before touching any state, so a rejected call leaves the
// accepted set, lastT and the accepted counter unchanged.
func (l *Limiter) Allow(t int64) (bool, error) {
	if t < 0 {
		return false, ErrNegativeTime
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen && t < l.lastT {
		return false, ErrClockRewind
	}
	l.q.EvictExpired(t)
	allowed := int64(l.q.InWindow(t)) < l.limit
	if allowed {
		l.q.Push(t)
		l.accept++
	}
	l.lastT = t
	l.seen = true
	return allowed, nil
}

// Accepted reports how many requests have been admitted so far.
func (l *Limiter) Accepted() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.accept
}

// Snapshot returns the currently retained (non-expired) accepted timestamps
// in ascending order; intended for SelfCheck and demonstration.
func (l *Limiter) Snapshot() []int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.q.Snapshot()
}
