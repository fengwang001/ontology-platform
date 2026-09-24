// Package api is the in-memory sliding-window MAX (amortized O(1), stdlib only).
package api

import (
	"errors"
	"math"
	"slices"
	"sync"

	"ontology/twin"
)

var (
	ErrCapacity = errors.New("slidingwindow: capacity must be positive") // W <= 0
	ErrEmpty    = errors.New("slidingwindow: window is empty")           // Evict/Max/Snapshot empty
	ErrNaN      = errors.New("slidingwindow: value must not be NaN")     // Push(All) NaN
)

var errInvariant = errors.New("slidingwindow: self-check invariant violated")

// Window is a fixed-width sliding window over float64. Use New.
type Window struct {
	mu sync.RWMutex
	w  int
	q  *twin.Queue
}

func New(W int) (*Window, error) {
	if W <= 0 {
		return nil, ErrCapacity
	}
	return &Window{w: W, q: twin.New()}, nil
}

// Push appends v and immediately evicts once if capacity is exceeded.
func (x *Window) Push(v float64) error { return x.PushAll([]float64{v}) }

// PushAll appends values in order; one NaN rejects the whole batch pre-mutation.
func (x *Window) PushAll(vs []float64) error {
	for _, v := range vs {
		if math.IsNaN(v) {
			return ErrNaN
		}
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, v := range vs {
		x.q.Push(v)
		if x.q.Len() > x.w {
			_, _ = x.q.Evict()
		}
	}
	return nil
}

func (x *Window) Evict() (v float64, err error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	var ok bool
	if v, ok = x.q.Evict(); !ok {
		err = ErrEmpty
	}
	return
}

func (x *Window) Max() (m float64, err error) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	var ok bool
	if m, ok = x.q.Max(); !ok {
		err = ErrEmpty
	}
	return
}

func (x *Window) Snapshot() (vs []float64, m float64, err error) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	if x.q.Len() == 0 {
		return nil, 0, ErrEmpty
	}
	m, _ = x.q.Max()
	return x.q.Values(), m, nil
}

// elevenOps encodes NOTES section three (W=4): digit=Push value, E=Evict.
var elevenOps = "313210E2EEE"

// SelfCheck replays elevenOps vs a naive FIFO (invariants 1-3 + FIFO prefix),
// then checks invariant 4. It never mutates x.
func (x *Window) SelfCheck() error {
	w, _ := New(4)
	var ref, evicted []float64
	for _, c := range elevenOps {
		got, had := 0.0, false
		if c == 'E' {
			v, err := w.Evict()
			if err != nil || v != ref[0] {
				return errInvariant
			}
			got, ref, had = v, ref[1:], true
		} else {
			v := float64(c - '0')
			if err := w.Push(v); err != nil {
				return err
			}
			ref = append(ref, v)
			if len(ref) > 4 {
				got, ref, had = ref[0], ref[1:], true
			}
		}
		if had {
			evicted = append(evicted, got)
		}
		vs, m, err := w.Snapshot()
		if err != nil || !slices.Equal(vs, ref) || m != slices.Max(ref) || !w.q.AggregatesValid() {
			return errInvariant // invariants 1 (contents/Max) and 3 (aggregates)
		}
	}
	if !slices.Equal(evicted, []float64{3, 1, 3, 2, 1, 0}) { // invariant 2
		return errInvariant
	}
	return checkFailures()
}

// checkFailures verifies invariant 4: distinct sentinels, rejected calls leave
// state untouched, and the window stays usable afterward.
func checkFailures() error {
	ok := ErrCapacity != ErrEmpty && ErrCapacity != ErrNaN && ErrEmpty != ErrNaN
	w0, e0 := New(0)
	ok = ok && w0 == nil && errors.Is(e0, ErrCapacity)
	empty, _ := New(2)
	_, zEv := empty.Evict()
	_, zMax := empty.Max()
	ok = ok && errors.Is(zEv, ErrEmpty) && errors.Is(zMax, ErrEmpty)
	w, _ := New(4)
	ok = ok && w.PushAll([]float64{5, 1, 5}) == nil
	before, bm, _ := w.Snapshot()
	ok = ok && errors.Is(w.Push(math.NaN()), ErrNaN)
	ok = ok && errors.Is(w.PushAll([]float64{1, math.NaN(), 2}), ErrNaN)
	after, am, _ := w.Snapshot()
	ok = ok && am == bm && slices.Equal(before, after)
	ok = ok && w.Push(9) == nil
	m9, _ := w.Max()
	ok = ok && m9 == 9
	if !ok {
		return errInvariant
	}
	return nil
}
