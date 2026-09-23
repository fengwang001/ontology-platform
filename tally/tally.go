// Package tally keeps a fixed-capacity window of recent call outcomes
// and maintains success/failure counts incrementally.
package tally

import (
	"errors"
	"sync"

	"ontology/outcome"
)

// ErrInvalidCapacity is returned when the window capacity is not positive.
var ErrInvalidCapacity = errors.New("tally: capacity must be positive")

// bucket aggregates the outcomes held by one ring slot.
type bucket struct {
	success int
	failure int
}

// Tally is a ring of buckets holding the most recent outcomes.
// Running totals are maintained incrementally, so Rate is O(1).
type Tally struct {
	mu       sync.Mutex
	buckets  []bucket
	head     int
	total    int
	failures int
	// lastVisits records how many buckets the most recent Rate call
	// touched. Diagnostics only; never part of the decision path.
	lastVisits int
}

// New returns a Tally with the given window capacity.
func New(capacity int) (*Tally, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Tally{buckets: make([]bucket, capacity)}, nil
}

// Record adds one outcome to the window, evicting the oldest when full.
// Rejected outcomes are ignored: a refused call carries no information.
func (t *Tally) Record(o outcome.Kind) {
	if o != outcome.Success && o != outcome.Failure {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	old := &t.buckets[t.head]
	t.total -= old.success + old.failure
	t.failures -= old.failure
	*old = bucket{}
	if o == outcome.Success {
		old.success = 1
	} else {
		old.failure = 1
		t.failures++
	}
	t.total++
	t.head = (t.head + 1) % len(t.buckets)
}

// Rate returns the failure ratio over the current window contents.
// Counts are incremental: no bucket is traversed here.
func (t *Tally) Rate() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastVisits = 0
	if t.total == 0 {
		return 0
	}
	return float64(t.failures) / float64(t.total)
}

// Total returns the number of outcomes currently in the window.
func (t *Tally) Total() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.total
}

// Failures returns the number of failures currently in the window.
func (t *Tally) Failures() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.failures
}

// Reset empties the window.
func (t *Tally) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buckets = make([]bucket, len(t.buckets))
	t.head = 0
	t.total = 0
	t.failures = 0
}

// Capacity returns the window capacity.
func (t *Tally) Capacity() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.buckets)
}

// LastRateVisits reports how many buckets the most recent Rate call
// visited. It is a diagnostics hook, not part of the tally contract.
func LastRateVisits(t *Tally) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastVisits
}
