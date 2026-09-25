// Package api is the public face of the dual-stream watermark aligner:
// New, Feed, Close, View, Dropped and SelfCheck. It depends only on package
// align (which in turn depends on package wm). All state is in-process
// memory and every method is safe for concurrent use.
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/align"
)

// Event is one stream record: Stream 'A'/'B', event-time TS.
type Event = align.Event

// Sentinel errors are mutually distinct and comparable with errors.Is.
var (
	ErrBadStream  = errors.New("api: stream must be 'A' or 'B'") // Stream not 'A'/'B'
	ErrNegativeTS = errors.New("api: timestamp must be >= 0")    // TS < 0
	ErrClosed     = errors.New("api: aligner already closed")    // Feed/Close after Close
)

// Aligner serializes concurrent calls and retains the cumulative emitted
// stream plus the late-event drop count.
type Aligner struct {
	mu      sync.RWMutex
	core    align.Aligner
	closed  bool
	emitted []Event
	dropped int
}

// New returns an aligner with both streams unseen (W = negative infinity).
func New() *Aligner { return &Aligner{core: *align.New()} }

// Feed validates ev first; on rejection no state changes. Accepted events are
// buffered and all events released by the new aligned watermark are returned
// in non-decreasing TS order. A late event returns (nil, nil) and is counted.
func (a *Aligner) Feed(ev Event) ([]Event, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil, ErrClosed
	}
	if ev.Stream != 'A' && ev.Stream != 'B' {
		return nil, ErrBadStream
	}
	if ev.TS < 0 {
		return nil, ErrNegativeTS
	}
	out, late := a.core.Feed(ev)
	if late {
		a.dropped++
		return nil, nil
	}
	a.emitted = append(a.emitted, out...)
	return append([]Event(nil), out...), nil // copy: caller must not alias state
}

// Close pushes both watermarks to positive infinity, flushes the buffer and
// marks the aligner closed. A second Close is rejected without effect.
func (a *Aligner) Close() ([]Event, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil, ErrClosed
	}
	out := a.core.Close()
	a.emitted = append(a.emitted, out...)
	a.closed = true
	return append([]Event(nil), out...), nil
}

// View returns a defensive copy of every event emitted so far.
func (a *Aligner) View() []Event {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]Event(nil), a.emitted...)
}

// Dropped returns the number of late events discarded.
func (a *Aligner) Dropped() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.dropped
}

// SelfCheck replays the built-in eight-event scenario on a fresh instance and
// verifies the four invariants (naive reorder, non-decreasing, no early
// emission, rejection leaves no trace). It never touches receiver state.
func (a *Aligner) SelfCheck() error {
	c := New()
	seq := []Event{{Stream: 'A', TS: 1}, {Stream: 'A', TS: 2}, {Stream: 'B', TS: 1}, {Stream: 'A', TS: 3}, {Stream: 'B', TS: 2}, {Stream: 'B', TS: 5}, {Stream: 'A', TS: 4}, {Stream: 'A', TS: 2}}
	want := [][]Event{nil, nil, {{Stream: 'A', TS: 1}, {Stream: 'B', TS: 1}}, nil, {{Stream: 'A', TS: 2}, {Stream: 'B', TS: 2}}, {{Stream: 'A', TS: 3}}, {{Stream: 'A', TS: 4}}, nil}
	for i, ev := range seq {
		got, err := c.Feed(ev)
		if err != nil || !equal(got, want[i]) {
			return fmt.Errorf("selfcheck: step %d got %v, %v want %v", i+1, got, err, want[i])
		}
	}
	rest, err := c.Close()
	if err != nil || !equal(rest, []Event{{Stream: 'B', TS: 5}}) || c.Dropped() != 1 {
		return fmt.Errorf("selfcheck: close got %v err=%v dropped=%d", rest, err, c.Dropped())
	}
	var wantAll []Event
	for _, r := range want {
		wantAll = append(wantAll, r...)
	}
	wantAll = append(wantAll, Event{Stream: 'B', TS: 5})
	if v := c.View(); !sorted(v) || !equal(v, wantAll) {
		return errors.New("selfcheck: emitted stream not non-decreasing / naive-equal")
	}
	bad := New()
	bad.Feed(Event{Stream: 'A', TS: 1}) // buffered: B unseen, nothing emitted yet
	for _, ev := range []Event{{Stream: 'C', TS: 1}, {Stream: 'A', TS: -1}} {
		if _, err := bad.Feed(ev); err == nil || len(bad.View()) != 0 || bad.Dropped() != 0 {
			return fmt.Errorf("selfcheck: rejection of %+v changed state", ev)
		}
	}
	if _, err := bad.Close(); err != nil || len(bad.View()) != 1 {
		return errors.New("selfcheck: rejected events must not appear after close")
	}
	return nil
}

func equal(a, b []Event) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sorted(v []Event) bool {
	for i := 1; i < len(v); i++ {
		if v[i].TS < v[i-1].TS {
			return false
		}
	}
	return true
}
