// Package lim implements the sliding-window decision logic on top of win.
// It validates the abstract monotonic clock and owns concurrency control.
package lim

import (
	"errors"
	"sync"

	"ontology/win"
)

// Sentinel errors for the two clock-related failures. They are distinct so
// callers can decide with errors.Is.
var (
	ErrNegativeTime  = errors.New("lim: timestamp must be >= 0")
	ErrClockRollback = errors.New("lim: timestamp must be >= last timestamp")
)

// Limiter admits at most limit requests in any half-open window of length
// window. Timestamps are supplied by an abstract monotonic integer clock.
type Limiter struct {
	mu       sync.Mutex
	limit    int64
	window   int64
	lastT    int64 // largest timestamp of a clock-valid Allow call
	accepted int64 // total admitted requests (never decreases)
	q        win.Queue
}

// New creates a Limiter. Parameters are assumed valid; the api layer is the
// single place that enforces limit >= 1 and window >= 1.
func New(limit, window int64) *Limiter {
	return &Limiter{limit: limit, window: window}
}

// Allow reports whether the request at timestamp t is admitted. On clock
// violations it returns a sentinel error and mutates nothing. A rate-limited
// request is not recorded, but it still advances the observed clock (lastT),
// because the timestamp itself is a valid time observation.
func (l *Limiter) Allow(t int64) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// All validation happens before any state change (I4: no trace on
	// failure): neither EvictExpired nor lastT runs on error.
	if t < 0 {
		return false, ErrNegativeTime
	}
	if t < l.lastT {
		return false, ErrClockRollback
	}
	l.lastT = t

	l.q.EvictExpired(t, l.window)
	// After eviction every retained entry satisfies ts >= t-window, and
	// monotonicity guarantees ts <= t. Len therefore counts the previously
	// accepted requests competing for this instant — including earlier
	// requests at the same timestamp t, which concurrent callers must be
	// charged for (see api.Test_ConcurrentAllow).
	if int64(l.q.Len()) < l.limit {
		l.q.Push(t)
		l.accepted++
		return true, nil
	}
	return false, nil
}

// Accepted reports the cumulative number of admitted requests.
func (l *Limiter) Accepted() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return int(l.accepted)
}

// Snapshot returns a copy of the currently retained accepted timestamps and
// the last observed timestamp, for invariant checks.
func (l *Limiter) Snapshot() (accepted []int64, lastT int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.q.Snapshot(), l.lastT
}
