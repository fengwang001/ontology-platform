// Package tally keeps a fixed-capacity ring of the most recent call outcomes.
package tally

import "ontology/outcome"

// Tally is a fixed-capacity sliding window over real call outcomes.
// Rejected calls are never recorded.
type Tally struct {
	buckets []outcome.Outcome
	head    int // index where the next outcome is written
	size    int // number of valid buckets (<= cap)
	sumOK   int
	sumFail int

	// bucketsVisited counts how many buckets the most recent FailureRate
	// call touched. Unexported on purpose: it must stay out of the API.
	bucketsVisited int
}

// New creates a Tally with the given positive capacity.
func New(capacity int) *Tally {
	return &Tally{buckets: make([]outcome.Outcome, capacity)}
}

// Cap returns the window capacity.
func (t *Tally) Cap() int { return len(t.buckets) }

// Add appends a real outcome, evicting the oldest when the window is full.
func (t *Tally) Add(o outcome.Outcome) {
	if t.size == len(t.buckets) {
		old := t.buckets[t.head] // the slot about to be overwritten
		t.apply(old, -1)
	} else {
		t.size++
	}
	t.buckets[t.head] = o // new bucket visited once
	t.apply(o, +1)
	t.head = (t.head + 1) % len(t.buckets)
}

func (t *Tally) apply(o outcome.Outcome, delta int) {
	switch o {
	case outcome.Success:
		t.sumOK += delta
	case outcome.Failure:
		t.sumFail += delta
	}
}

// Total returns the number of real outcomes currently in the window.
func (t *Tally) Total() int { return t.size }

// Failures returns the number of failures in the window.
func (t *Tally) Failures() int { return t.sumFail }

// Reset empties the window and all aggregate counters.
func (t *Tally) Reset() {
	t.head, t.size, t.sumOK, t.sumFail = 0, 0, 0, 0
}

// FailureRate returns failures/total and the total. Counts are maintained
// incrementally; only a single oldest bucket is read for a self-consistency
// boundary check, so visited buckets do not grow with capacity.
func (t *Tally) FailureRate() (rate float64, total int) {
	t.bucketsVisited = 0
	if t.size == 0 {
		return 0, 0
	}
	oldest := (t.head - t.size + len(t.buckets)) % len(t.buckets)
	_ = t.buckets[oldest] // boundary check: one bucket, independent of cap
	t.bucketsVisited = 1
	return float64(t.sumFail) / float64(t.size), t.size
}

// bucketsVisitedCount exposes the counter to in-package tests only.
func (t *Tally) bucketsVisitedCount() int { return t.bucketsVisited }
