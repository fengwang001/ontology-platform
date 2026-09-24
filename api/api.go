// Package api is the concurrency-safe entry point of the session window.
// It depends on sess (and transitively evt); the dependency direction is
// strictly one way: api -> sess -> evt.
package api

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"ontology/evt"
	"ontology/sess"
)

// The three mutually distinct, decidable sentinel errors.
var (
	// ErrInvalidGap: New was called with a non-positive gap.
	ErrInvalidGap = errors.New("api: gap must be a positive integer")
	// ErrTooManySessions: a feed would exceed the per-key session cap.
	ErrTooManySessions = sess.ErrTooManySessions
	// ErrInvalidEvent: an event has an empty Key.
	ErrInvalidEvent = evt.ErrInvalidEvent
)

// Window maintains sessions for all keys in process memory.
type Window struct {
	mu  sync.RWMutex
	set *sess.Set
}

// New creates a window with gap > 0 and a per-key session cap
// (maxSessions <= 0 means unlimited).
func New(gap int64, maxSessions int) (*Window, error) {
	if gap <= 0 {
		return nil, ErrInvalidGap
	}
	return &Window{set: sess.NewSet(gap, maxSessions)}, nil
}

// Feed adds a batch of events atomically: if any event is rejected, no event
// of the batch takes effect.
func (w *Window) Feed(evs []evt.Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.set.AddAll(evs)
}

// Snapshot returns the canonical session sequence of one key. Safe for
// concurrent use; the returned slice is an independent copy.
func (w *Window) Snapshot(key string) []sess.Session {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.set.Sessions(key)
}

// sixStepWant is the six-row table derived in NOTES.md (gap=10).
var sixStepWant = [][]sess.Session{
	{{Start: 100, End: 100, N: 1}},
	{{Start: 100, End: 105, N: 2}},
	{{Start: 100, End: 105, N: 2}, {Start: 130, End: 130, N: 1}},
	{{Start: 100, End: 105, N: 2}, {Start: 130, End: 135, N: 2}},
	{{Start: 100, End: 105, N: 2}, {Start: 118, End: 118, N: 1}, {Start: 130, End: 135, N: 2}},
	{{Start: 100, End: 105, N: 3}, {Start: 118, End: 118, N: 1}, {Start: 130, End: 135, N: 2}},
}

// recompute is the sorted full-rescan oracle: collect, sort, scan once.
func recompute(ts []int64, gap int64) []sess.Session {
	cp := append([]int64(nil), ts...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	var out []sess.Session
	for _, x := range cp {
		if n := len(out); n > 0 && x-out[n-1].End <= gap {
			out[n-1].End, out[n-1].N = x, out[n-1].N+1
		} else {
			out = append(out, sess.Session{Start: x, End: x, N: 1})
		}
	}
	return out
}

// SelfCheck verifies all four invariants against built-in event sequences.
// It is read-only with respect to the receiver (it exercises local sets), so
// many goroutines may call it concurrently with Snapshot.
func (w *Window) SelfCheck() error {
	// Invariant 1: incremental maintenance matches the six-step table.
	s := sess.NewSet(10, 0)
	for i, ts := range []int64{100, 105, 130, 135, 118, 100} {
		if err := s.Add(evt.Event{Key: "k", TS: ts}); err != nil {
			return err
		}
		if got := s.Sessions("k"); !reflect.DeepEqual(got, sixStepWant[i]) {
			return fmt.Errorf("api: six-step mismatch at step %d: %v", i+1, got)
		}
	}
	// Invariants 1+2+3: a shuffled feed equals a sorted rescan and is canonical.
	multiset := []int64{100, 105, 130, 135, 118, 100, 1, 200, 111, 134}
	want := recompute(multiset, 10)
	s2 := sess.NewSet(10, 0)
	for _, p := range []int{8, 0, 5, 9, 2, 7, 4, 1, 6, 3} { // fixed shuffle
		if err := s2.Add(evt.Event{Key: "k", TS: multiset[p]}); err != nil {
			return err
		}
	}
	got := s2.Sessions("k")
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("api: order invariance mismatch: %v", got)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Start-got[i-1].End <= 10 {
			return errors.New("api: canonical spacing violated")
		}
	}
	// Invariant 4: every rejection is a distinct sentinel and leaves no trace.
	c, err := New(10, 1)
	if err != nil {
		return err
	}
	if err := c.Feed([]evt.Event{{Key: "k", TS: 0}}); err != nil {
		return err
	}
	beforeK, beforeOther := c.Snapshot("k"), c.Snapshot("other")
	batches := []struct {
		evs []evt.Event
		err error
	}{
		{[]evt.Event{{Key: "", TS: 1}}, ErrInvalidEvent},
		{[]evt.Event{{Key: "k", TS: 100}}, ErrTooManySessions},
		{[]evt.Event{{Key: "other", TS: 0}, {Key: "", TS: 1}}, ErrInvalidEvent},
	}
	for i, b := range batches {
		if err := c.Feed(b.evs); !errors.Is(err, b.err) {
			return fmt.Errorf("api: batch %d err = %v, want %v", i, err, b.err)
		}
		if !reflect.DeepEqual(c.Snapshot("k"), beforeK) ||
			!reflect.DeepEqual(c.Snapshot("other"), beforeOther) {
			return fmt.Errorf("api: batch %d left a trace", i)
		}
	}
	return nil
}
