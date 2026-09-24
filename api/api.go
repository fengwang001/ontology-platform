// Package api is the outward face of the count-based tumbling window
// aggregator: validated inputs, concurrency-safe, no state change on failure.
package api

import (
	"errors"
	"slices"
	"sync"

	"ontology/cwagg"
)

var (
	ErrSize     = errors.New("api: size must be positive")
	ErrLateness = errors.New("api: lateness must be non-negative")
	ErrPos      = errors.New("api: pos must be non-negative")
	ErrKey      = errors.New("api: key must be non-empty")
)

// Event is one upstream change.
type Event = cwagg.Event

// Fire is the aggregate emitted once when a window closes.
type Fire = cwagg.Fire

// Agg is a concurrency-safe aggregator.
type Agg struct {
	mu  sync.RWMutex
	agg *cwagg.Agg
}

// New validates size and lateness before creating anything.
func New(size, lateness int64) (*Agg, error) {
	if size <= 0 {
		return nil, ErrSize
	}
	if lateness < 0 {
		return nil, ErrLateness
	}
	return &Agg{agg: cwagg.New(size, lateness)}, nil
}

// Feed validates the whole batch before applying it; a rejected batch
// leaves no trace. On success it returns the fires of this batch.
func (a *Agg) Feed(evs []Event) ([]Fire, error) {
	for _, e := range evs {
		if e.Pos < 0 {
			return nil, ErrPos
		}
		if e.Key == "" {
			return nil, ErrKey
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	base := len(a.agg.Fired())
	for _, e := range evs {
		a.agg.Add(e)
	}
	return a.agg.Fired()[base:], nil
}

// Fired returns a copy of all fires so far, in emission order.
func (a *Agg) Fired() []Fire {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.agg.Fired()
}

// Dropped returns the number of discarded elements.
func (a *Agg) Dropped() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.agg.Dropped()
}

var errSelfCheck = errors.New("api: self-check failed")

// SelfCheck verifies the four invariants on built-in sequences, using only
// fresh instances; it never touches the receiver's state.
func (a *Agg) SelfCheck() error {
	g, err := New(5, 2) // invariants 1-3: the section-3 sequence
	if err != nil {
		return err
	}
	ev := func(p, v int64) Event { return Event{Key: "k", Pos: p, Val: v} }
	evs := []Event{ev(0, 10), ev(1, 20), ev(2, 30), ev(3, 40), ev(4, 50), ev(5, 60), ev(9, 90), ev(7, 70)}
	if _, err = g.Feed(evs); err != nil {
		return err
	}
	if !slices.Equal(g.Fired(), []Fire{{Key: "k", Win: 0, Sum: 150}}) || g.Dropped() != 0 {
		return errSelfCheck
	}
	g2, err := New(7, 0) // invariant 1: generated traffic vs batch recompute
	if err != nil {
		return err
	}
	var gen []Event
	for p := int64(0); p < 50; p++ {
		for _, k := range []string{"a", "b"} {
			gen = append(gen, Event{Key: k, Pos: p, Val: p*3 + int64(len(k))})
		}
	}
	if _, err = g2.Feed(gen); err != nil {
		return err
	}
	if !matchesBatch(g2.Fired(), gen, 7) {
		return errSelfCheck
	}
	nf, nd := len(g.Fired()), g.Dropped() // invariant 4: rejection leaves no trace
	if _, err = g.Feed([]Event{{Key: "k", Pos: -1, Val: 1}}); !errors.Is(err, ErrPos) {
		return errSelfCheck
	}
	if len(g.Fired()) != nf || g.Dropped() != nd {
		return errSelfCheck
	}
	return nil
}

// matchesBatch reports whether fs equals the batch recomputation over evs.
func matchesBatch(fs []Fire, evs []Event, size int64) bool {
	type kw struct {
		k string
		w int64
	}
	sum, cnt, got := map[kw]int64{}, map[kw]int64{}, map[kw]int64{}
	for _, e := range evs {
		id := kw{e.Key, e.Pos / size}
		sum[id] += e.Val
		cnt[id]++
	}
	for _, f := range fs {
		id := kw{f.Key, f.Win}
		if _, dup := got[id]; dup {
			return false
		}
		got[id] = f.Sum
	}
	full := 0
	for id, c := range cnt {
		if c != size {
			continue
		}
		full++
		if got[id] != sum[id] {
			return false
		}
	}
	return full == len(got)
}
