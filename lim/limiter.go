// Package lim drives a tkn bucket with an abstract monotonic integer
// clock: it validates timestamps and demands, computes elapsed time, and
// serializes concurrent access.
package lim

import (
	"errors"
	"sync"

	"ontology/tkn"
)

// Sentinel errors. They are deliberately distinct values so callers can
// discriminate the two kinds of failed precondition with errors.Is.
var (
	// ErrInvalidNeed is returned when need <= 0.
	ErrInvalidNeed = errors.New("lim: need must be at least 1")
	// ErrClockRollback is returned when a timestamp is older than the
	// timestamp of a previously accepted request.
	ErrClockRollback = errors.New("lim: non-monotonic timestamp")
)

// Limiter is a single-bucket rate limiter keyed on an abstract clock.
// It is safe for concurrent use.
type Limiter struct {
	mu     sync.Mutex
	bucket *tkn.Bucket
	last   int64
}

// New creates a limiter with a full bucket. capacity and rate must be
// positive; the public api layer enforces that before calling New.
func New(capacity, rate int64) *Limiter {
	return &Limiter{bucket: tkn.New(capacity, rate)}
}

// Allow decides whether a request needing `need` tokens at timestamp t is
// admitted. Precondition failures (need <= 0 or t < last) return a
// non-nil error and leave tokens and last untouched.
func (l *Limiter) Allow(t, need int64) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Validate fully before touching any state (failure leaves no trace).
	if need <= 0 {
		return false, ErrInvalidNeed
	}
	if t < l.last {
		return false, ErrClockRollback
	}
	if t > l.last {
		l.bucket.Refill(t - l.last)
		l.last = t
	}
	return l.bucket.TryConsume(need), nil
}

// Tokens returns the current token count (0 <= Tokens <= capacity).
func (l *Limiter) Tokens() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.bucket.Tokens()
}

// RefillIsO1 reports whether the most recent refill used O(1) work, i.e.
// the internal per-token increment counter never moved. Only this boolean
// crosses the package boundary; the raw counter value is never exposed.
func (l *Limiter) RefillIsO1() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.bucket.RefillIsO1()
}
