// Package api is the public facade of the change-stream rate limiter:
// construction, Feed, Dropped and SelfCheck. It depends only on lim.
package api

import (
	"fmt"

	"ontology/lim"
)

// Event is a change-stream event arriving at integer tick TS.
type Event = lim.Event

// Sentinel errors are pairwise distinct and decidable via errors.Is.
var (
	ErrInvalidParam = lim.ErrInvalidParam
	ErrTSRollback   = lim.ErrTSRollback
	ErrEmptyKey     = lim.ErrEmptyKey
)

// Limiter is the concurrency-safe public limiter.
type Limiter struct {
	inner *lim.Limiter
}

// New constructs a limiter with a full bucket of capacity B and refill r.
func New(B, r int) (*Limiter, error) {
	l, err := lim.New(B, r)
	if err != nil {
		return nil, err
	}
	return &Limiter{inner: l}, nil
}

// Feed processes one batch and returns each event's admission tick.
func (l *Limiter) Feed(events []Event) ([]int64, error) {
	return l.inner.Feed(events)
}

// Dropped reports dropped events; correct backpressure keeps it at 0.
func (l *Limiter) Dropped() int { return l.inner.Dropped() }

// naive is the tick-by-tick reference: per-tick refill capped at B and a
// FIFO queue served one token at a time.
func naive(B, r int, evs []Event) []int64 {
	admit := make([]int64, len(evs))
	tokens, t := int64(B), evs[0].TS
	var q []int
	serve := func(tick int64) {
		for len(q) > 0 && tokens > 0 {
			tokens--
			admit[q[0]] = tick
			q = q[1:]
		}
	}
	for i, e := range evs {
		for tick := t + 1; tick <= e.TS; tick++ {
			if r > 0 {
				tokens = min(int64(B), tokens+int64(r))
			}
			serve(tick)
		}
		t = e.TS
		if tokens > 0 {
			tokens--
			admit[i] = e.TS
		} else {
			q = append(q, i)
		}
	}
	for tick := t + 1; len(q) > 0; tick++ {
		if r == 0 {
			for _, idx := range q {
				admit[idx] = lim.Never
			}
			break
		}
		tokens = min(int64(B), tokens+int64(r))
		serve(tick)
	}
	return admit
}

// SelfCheck exercises built-in sequences (including the canonical six-event
// burst case) and verifies all four invariants. It mutates no state of l.
func (l *Limiter) SelfCheck() error {
	seqs := [][]Event{
		{{TS: 0, Key: "k1"}, {TS: 0, Key: "k2"}, {TS: 0, Key: "k3"}, {TS: 1, Key: "k4"}, {TS: 1, Key: "k5"}, {TS: 4, Key: "k6"}},
		{{TS: 0, Key: "a"}, {TS: 0, Key: "b"}, {TS: 2, Key: "c"}, {TS: 2, Key: "d"}, {TS: 2, Key: "e"}, {TS: 5, Key: "f"}},
		{{TS: 10, Key: "x"}, {TS: 10, Key: "y"}, {TS: 10, Key: "z"}, {TS: 10, Key: "w"}},
	}
	for _, evs := range seqs {
		c, err := New(3, 1)
		if err != nil {
			return err
		}
		got, err := c.Feed(evs)
		if err != nil {
			return err
		}
		want := naive(3, 1, evs)
		for i := range got {
			if got[i] != want[i] {
				return fmt.Errorf("invariant 1: seq %v got %v want %v", evs, got, want)
			}
			if got[i] < evs[i].TS {
				return fmt.Errorf("invariant 3: admit %d before TS %d", got[i], evs[i].TS)
			}
			if i > 0 && got[i] < got[i-1] {
				return fmt.Errorf("invariant 3: admission not non-decreasing: %v", got)
			}
		}
		if c.Dropped() != 0 || len(got) != len(evs) {
			return fmt.Errorf("invariant 2: dropped=%d admitted=%d", c.Dropped(), len(got))
		}
	}
	// Invariant 4: rejected operations leave no trace.
	c, err := New(1, 1)
	if err != nil {
		return err
	}
	base, _ := c.Feed([]Event{{TS: 0, Key: "ok"}})
	if _, e := c.Feed([]Event{{TS: 1, Key: "bad"}, {TS: 0, Key: "late"}}); e != ErrTSRollback {
		return fmt.Errorf("invariant 4: rollback error = %v", e)
	}
	if _, e := c.Feed([]Event{{TS: 2, Key: ""}}); e != ErrEmptyKey {
		return fmt.Errorf("invariant 4: empty-key error = %v", e)
	}
	after, e := c.Feed([]Event{{TS: 2, Key: "fine"}})
	if e != nil || len(after) != 1 || after[0] != 2 || base[0] != 0 {
		return fmt.Errorf("invariant 4: state changed after rejection")
	}
	if _, e := New(0, 1); e != ErrInvalidParam {
		return fmt.Errorf("invariant 4: param error = %v", e)
	}
	if _, e := New(1, -1); e != ErrInvalidParam {
		return fmt.Errorf("invariant 4: rate error = %v", e)
	}
	return nil
}
