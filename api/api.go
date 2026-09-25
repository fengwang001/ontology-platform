// Package api is the public face of the session-window pipeline.
package api

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"sync"

	"ontology/sess"
	"ontology/swin"
)

type (
	Event   = swin.Event
	Session = sess.Session
)

var ErrNonPositiveGap = swin.ErrNonPositiveGap
var ErrEmptyKey = swin.ErrEmptyKey
var ErrTooManyOpen = swin.ErrTooManyOpen

// API serializes access to one in-memory engine.
type API struct {
	mu  sync.RWMutex
	eng *swin.Engine
}

func New(g int64, maxOpen int) (*API, error) {
	e, err := swin.NewEngine(g, maxOpen)
	return &API{eng: e}, err
}

// Feed applies a batch atomically and returns the flattened view.
func (a *API) Feed(evs []Event) ([]Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.eng.Feed(evs); err != nil {
		return nil, err
	}
	var flat []Session
	for _, ss := range a.eng.View() {
		flat = append(flat, ss...)
	}
	sort.Slice(flat, func(i, j int) bool { return flat[i].Start < flat[j].Start })
	return flat, nil
}

func (a *API) View() map[string][]Session {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.eng.View()
}

func (a *API) Dropped() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.eng.Dropped()
}

func ev(k string, t int64) Event { return Event{Key: k, TS: t} }

func same3(x, y []Session) bool { // compare (Start, End, Count) only
	return slices.EqualFunc(x, y, func(p, q Session) bool {
		return p.Start == q.Start && p.End == q.End && p.Count == q.Count
	})
}

// SelfCheck verifies the four invariants on a built-in sequence and the
// binary-lookup cost bound, on fresh instances only; receiver untouched.
func (a *API) SelfCheck() error {
	const g int64 = 3
	seq := []Event{ev("K", 10), ev("K", 13), ev("K", 20), ev("K", 16), ev("K", 23), ev("K", 17), ev("K", 25), ev("K", 11)}
	f, err := New(g, 1000)
	if err != nil {
		return err
	}
	acc := map[string][]int64{}
	wm := int64(math.MinInt64)
	for i, e := range seq {
		pred := false
		if e.TS < wm { // predict the drop from the pre-feed closed view
			for _, s := range f.View()[e.Key] {
				if s.Closed && s.Start-g <= e.TS && e.TS <= s.End+g {
					pred = true
				}
			}
		}
		d0 := f.Dropped()
		if _, err := f.Feed([]Event{e}); err != nil {
			return err
		}
		if drop := f.Dropped() != d0; drop != pred {
			return fmt.Errorf("selfcheck step %d: drop mismatch", i)
		}
		if e.TS > wm {
			wm = e.TS
		}
		if f.Dropped() == d0 {
			acc[e.Key] = append(acc[e.Key], e.TS)
		}
	}
	view := f.View() // invariant 1: view == batch recompute on accepted events
	for k, got := range view {
		if !same3(got, sess.Link(acc[k], g)) {
			return errors.New("selfcheck: view != batch recompute")
		}
	}
	var frozen []Session // invariant 2: closed rows never mutate afterwards
	for _, ss := range view {
		for _, s := range ss {
			if s.Closed {
				frozen = append(frozen, s)
			}
		}
	}
	if _, err := f.Feed([]Event{ev("Z", 1), ev("K", 12), ev("K", 1000)}); err != nil {
		return err
	}
	for _, w := range frozen {
		var got Session
		for _, s := range f.View()["K"] {
			if s.Start == w.Start {
				got = s
			}
		}
		if !got.Closed || got != w {
			return errors.New("selfcheck: closed session mutated")
		}
	}
	if _, err := New(0, 1); !errors.Is(err, ErrNonPositiveGap) { // invariant 4
		return errors.New("selfcheck: non-positive gap accepted")
	}
	f2, _ := New(g, 1)
	if _, err := f2.Feed([]Event{ev("A", 1), ev("", 2)}); !errors.Is(err, ErrEmptyKey) {
		return errors.New("selfcheck: empty key accepted")
	}
	if _, err := f2.Feed([]Event{ev("A", 1), ev("B", 4)}); !errors.Is(err, ErrTooManyOpen) {
		return errors.New("selfcheck: maxOpen not enforced")
	}
	if len(f2.View()) != 0 || f2.Dropped() != 0 {
		return errors.New("selfcheck: rejected feed left state behind")
	}
	if _, err := f2.Feed([]Event{ev("A", 1)}); err != nil {
		return errors.New("selfcheck: unusable after rejection")
	}
	return swin.VerifyLookup()
}
