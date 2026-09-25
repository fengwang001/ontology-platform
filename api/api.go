// Package api is the public face of the watermark window joiner.
package api

import (
	"fmt"

	"ontology/win"
	"ontology/wjoin"
)

// Event is one input record; Join is one emitted pair.
type Event = wjoin.Event
type Join = wjoin.Join

var ErrBadWindow = wjoin.ErrBadWindow
var ErrBadDelay = wjoin.ErrBadDelay
var ErrEmptyKey = wjoin.ErrEmptyKey
var ErrBadSide = wjoin.ErrBadSide

// Joiner is the concurrent-safe public handle.
type Joiner struct{ j *wjoin.Joiner }

// New fails as a whole on non-positive W or negative delay.
func New(w, delay int64) (*Joiner, error) {
	j, err := wjoin.New(w, delay)
	if err != nil {
		return nil, err
	}
	return &Joiner{j}, nil
}

func (a *Joiner) Feed(evs []Event) ([]Join, error) { return a.j.Feed(evs) }
func (a *Joiner) Flush()                           { a.j.Flush() }
func (a *Joiner) Joins() []Join                    { return a.j.Joins() }
func (a *Joiner) Retained() int                    { return a.j.Retained() }
func (a *Joiner) Dropped() int                     { return a.j.Dropped() }

// Batch is the reference for invariant 1: replay evs to find the accepted
// ones, then emit the grouped L×R cross product per (Key, window).
func Batch(w, delay int64, evs []Event) []Join {
	type bk struct {
		key   string
		start int64
	}
	type sides struct{ l, r []int64 }
	groups := map[bk]*sides{}
	wm, seen := int64(0), false
	for _, e := range evs {
		if !seen || e.TS-delay > wm {
			wm, seen = e.TS-delay, true
		}
		w := win.Of(e.TS, w)
		if w.Late(wm) {
			continue
		}
		k := bk{e.Key, w.Start}
		g := groups[k]
		if g == nil {
			g = &sides{}
			groups[k] = g
		}
		if e.Side == 'L' {
			g.l = append(g.l, e.TS)
		} else {
			g.r = append(g.r, e.TS)
		}
	}
	var out []Join
	for k, g := range groups {
		for _, l := range g.l {
			for _, r := range g.r {
				out = append(out, Join{Key: k.key, WindowStart: k.start, LeftTS: l, RightTS: r})
			}
		}
	}
	return out
}

// ev builds an Event with keyed fields (keeps vet's composites check happy).
func ev(key string, ts int64, side byte) Event { return Event{Key: key, TS: ts, Side: side} }

// sameJoins compares two join lists as multisets.
func sameJoins(a, b []Join) bool {
	if len(a) != len(b) {
		return false
	}
	c := map[Join]int{}
	for _, j := range a {
		c[j]++
	}
	for _, j := range b {
		if c[j]--; c[j] < 0 {
			return false
		}
	}
	return true
}

// SelfCheck verifies the four invariants on built-in sequences, each against
// a fresh joiner; it never touches the receiver's state.
func (a *Joiner) SelfCheck() error {
	seqs := []struct {
		w, d int64
		evs  []Event
	}{
		{10, 3, []Event{ev("k", 5, 'L'), ev("k", 6, 'R'), ev("k", 12, 'L'), ev("k", 8, 'R'), ev("k", 2, 'L'), ev("k", 15, 'R')}},
		{10, 0, []Event{ev("a", -5, 'L'), ev("a", 5, 'R'), ev("b", -6, 'R'), ev("a", -4, 'R'), ev("b", -7, 'L')}},
		{10, 2, []Event{ev("x", 100, 'L'), ev("x", 50, 'R'), ev("x", 95, 'R'), ev("x", 101, 'R'), ev("y", 101, 'L')}},
	}
	for i, s := range seqs { // invariant 1+2: flush == batch, no duplicates
		j, err := New(s.w, s.d)
		if err != nil {
			return err
		}
		if _, err := j.Feed(s.evs); err != nil {
			return err
		}
		j.Flush()
		got := j.Joins()
		if !sameJoins(got, Batch(s.w, s.d, s.evs)) {
			return fmt.Errorf("selfcheck seq %d: stream joins != batch cross product", i)
		}
		seen := map[Join]bool{}
		for _, jn := range got {
			if seen[jn] {
				return fmt.Errorf("selfcheck seq %d: duplicate join %+v", i, jn)
			}
			seen[jn] = true
		}
	}
	j, _ := New(10, 0) // invariant 3: watermark never regresses
	j.Feed([]Event{ev("k", 100, 'L'), ev("k", 50, 'R'), ev("k", 95, 'R')})
	if j.Dropped() != 2 || j.Retained() != 1 || len(j.Joins()) != 0 {
		return fmt.Errorf("selfcheck: watermark regressed (dropped=%d retained=%d)", j.Dropped(), j.Retained())
	}
	j, _ = New(10, 3) // invariant 4: rejected operations leave no trace
	j.Feed([]Event{ev("k", 5, 'L'), ev("k", 6, 'R')})
	for _, evs := range [][]Event{{ev("", 1, 'L')}, {ev("k", 1, 'X')}} {
		if _, err := j.Feed(evs); err == nil {
			return fmt.Errorf("selfcheck: invalid event accepted")
		}
	}
	if len(j.Joins()) != 1 || j.Dropped() != 0 || j.Retained() != 2 {
		return fmt.Errorf("selfcheck: rejected feed left a trace")
	}
	if _, err := j.Feed([]Event{ev("k", 8, 'R')}); err != nil || len(j.Joins()) != 2 {
		return fmt.Errorf("selfcheck: joiner unusable after rejection")
	}
	return nil
}
