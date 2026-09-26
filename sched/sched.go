// Package sched is the submitter sitting on top of the lb core. It owns the
// monotonic clock (lastT), validates every timestamp, and serializes access
// so Submit/InSystem are safe for concurrent use.
package sched

import (
	"errors"
	"sync"

	"ontology/lb"
)

// Sentinel errors. They are mutually distinct so callers can use errors.Is
// to tell configuration problems from clock problems.
var (
	// ErrInvalidConfig is returned when capacity <= 0 or interval <= 0.
	ErrInvalidConfig = errors.New("sched: capacity and interval must both be >= 1")
	// ErrNegativeTime is returned when a Submit carries t < 0.
	ErrNegativeTime = errors.New("sched: submit timestamp must be >= 0")
	// ErrClockRewind is returned when t is smaller than a previous timestamp.
	ErrClockRewind = errors.New("sched: submit timestamp must be non-decreasing")
)

// Submitter validates submissions and forwards them to a leaky bucket.
type Submitter struct {
	mu     sync.Mutex
	bucket *lb.Bucket
	lastT  int64
}

// New creates a Submitter. Both parameters must be >= 1.
func New(capacity, interval int64) (*Submitter, error) {
	if capacity <= 0 || interval <= 0 {
		return nil, ErrInvalidConfig
	}
	return &Submitter{bucket: lb.New(capacity, interval)}, nil
}

// Submit processes one request arriving at monotonic timestamp t.
// On success it returns the assigned departure time and admitted=true.
// When the bucket is full it returns (0, false, nil): rejection is a
// normal result, not an error, and no item is appended. Invalid t values
// return a sentinel error and leave every bit of state untouched because
// validation strictly precedes Drain/Admit.
func (s *Submitter) Submit(t int64) (dep int64, admitted bool, err error) {
	if t < 0 {
		return 0, false, ErrNegativeTime // before touching lastT or the queue
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if t < s.lastT {
		return 0, false, ErrClockRewind // strictly before any state change
	}
	s.bucket.Drain(t) // items departed by t are gone regardless of admission
	s.lastT = t       // the clock advances on every well-formed Submit
	dep, full := s.bucket.Admit(t)
	if full {
		return 0, false, nil
	}
	return dep, true, nil
}

// InSystem reports how many admitted requests have not leaked out yet.
func (s *Submitter) InSystem() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bucket.InSystem()
}

// LastT reports the greatest timestamp seen on a well-formed Submit.
func (s *Submitter) LastT() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastT
}

// SelfCheck runs the internal complexity self-check of the underlying
// bucket. Only a pass/fail error crosses layers; internal counters do not.
func SelfCheck() error { return lb.SelfCheck() }
