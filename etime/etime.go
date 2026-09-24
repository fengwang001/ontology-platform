// Package etime maintains the event-time watermark: maxEt, ETW = maxEt - L,
// and lateness judgment. It knows nothing about processing time.
package etime

import (
	"errors"
	"math"
)

// ErrNegativeLateness is returned when lateness L is negative.
var ErrNegativeLateness = errors.New("etime: lateness must be >= 0")

// negInf stands for "negative infinity": the watermark before any event.
const negInf = math.MinInt64

// Clock is the event-time watermark state. Not safe for concurrent use;
// callers (package dual) serialize access.
type Clock struct {
	lateness int64
	maxEt    int64
	seen     bool
	// checked counts how many events the most recent Ingest examined
	// one by one to judge lateness and advance maxEt/ETW. It is an
	// unexported complexity probe: maxEt is a single integer maintained
	// incrementally, so an Ingest must never scan the accepted set.
	checked int
}

// New returns a Clock with allowed lateness l (a constant).
func New(l int64) (*Clock, error) {
	if l < 0 {
		return nil, ErrNegativeLateness
	}
	return &Clock{lateness: l}, nil
}

// Ingest judges one event time and, when on time, advances maxEt/ETW.
// It reports whether the event is on time. Late events change nothing.
func (c *Clock) Ingest(et int64) bool {
	c.checked = 1 // only the arriving event itself is examined
	if c.seen && et <= c.ETW() {
		return false
	}
	if !c.seen || et > c.maxEt {
		c.maxEt = et
	}
	c.seen = true
	return true
}

// ETW returns the event-time watermark: maxEt - lateness, or negInf
// before any event has been seen. Monotonic non-decreasing.
func (c *Clock) ETW() int64 {
	if !c.seen {
		return negInf
	}
	return c.maxEt - c.lateness
}

// MaxEt returns the largest event time seen so far, or negInf if none.
func (c *Clock) MaxEt() int64 {
	if !c.seen {
		return negInf
	}
	return c.maxEt
}

// NegInf is the negative-infinity watermark value.
func NegInf() int64 { return negInf }

// SelfCheck verifies the incremental-maintenance invariant from within the
// package: after m on-time ingests, one more on-time ingest examines only a
// small constant number of events, independent of m. It exposes only a
// boolean verdict, never the counter value.
func SelfCheck() bool {
	for _, m := range []int{100, 1000, 10000} {
		c, err := New(5)
		if err != nil {
			return false
		}
		for i := 0; i < m; i++ {
			if !c.Ingest(int64(i)) {
				return false
			}
		}
		if !c.Ingest(int64(m)) {
			return false
		}
		if c.checked > 2 { // small constant, not O(m)
			return false
		}
	}
	return true
}
