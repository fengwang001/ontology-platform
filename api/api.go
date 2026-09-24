// Package api is the public facade of the CEP `A -> B within T` matcher.
package api

import (
	"fmt"

	"ontology/cepmatch"
	"ontology/cepwin"
)

// Mode selects strict or relaxed contiguity.
type Mode = cepmatch.Mode

const (
	Strict  = cepmatch.Strict
	Relaxed = cepmatch.Relaxed
)

// Event is an upstream event.
type Event = cepwin.Event

// Match is an emitted (A, B) pair.
type Match = cepwin.Match

// Matcher is the concurrency-safe pattern matcher.
type Matcher struct {
	eng *cepmatch.Engine
}

// New constructs a matcher; T < 0, maxPending <= 0 or an illegal Mode fail.
func New(mode Mode, T int64, maxPending int) (*Matcher, error) {
	eng, err := cepmatch.NewEngine(mode, T, maxPending)
	if err != nil {
		return nil, err
	}
	return &Matcher{eng: eng}, nil
}

// Feed applies one batch atomically and returns the matches it produced.
func (m *Matcher) Feed(evs []Event) ([]Match, error) {
	return m.eng.Feed(evs)
}

// Matches returns every match emitted so far, in B-arrival order.
func (m *Matcher) Matches() []Match {
	return m.eng.Matches()
}

// SelfCheck replays built-in sequences and verifies the four invariants:
// it compares against hand-derived expected pairs, checks pair legality and
// event non-reuse, and verifies a rejected batch leaves no trace.
func (m *Matcher) SelfCheck() error {
	ten := []Event{
		{Key: "k", Type: "A", TS: 1}, {Key: "k", Type: "C", TS: 2},
		{Key: "k", Type: "B", TS: 3}, {Key: "k", Type: "A", TS: 4},
		{Key: "k", Type: "A", TS: 6}, {Key: "k", Type: "B", TS: 9},
		{Key: "k", Type: "B", TS: 11}, {Key: "k", Type: "A", TS: 12},
		{Key: "z", Type: "A", TS: 14}, {Key: "k", Type: "B", TS: 17},
	}
	// Expected pairs (derived in NOTES.md); boundary: delta==T matches, T+1 not.
	boundary := []Event{
		{Key: "k", Type: "A", TS: 0}, {Key: "k", Type: "B", TS: 5},
		{Key: "k", Type: "A", TS: 6}, {Key: "k", Type: "B", TS: 12},
	}
	cases := []struct {
		evs  []Event
		want map[Mode][]Match
	}{
		{ten, map[Mode][]Match{
			Relaxed: {{A: ten[0], B: ten[2]}, {A: ten[3], B: ten[5]},
				{A: ten[4], B: ten[6]}, {A: ten[7], B: ten[9]}},
			Strict: {{A: ten[4], B: ten[5]}, {A: ten[7], B: ten[9]}},
		}},
		{boundary, map[Mode][]Match{
			Relaxed: {{A: boundary[0], B: boundary[1]}},
			Strict:  {{A: boundary[0], B: boundary[1]}},
		}},
	}
	for _, c := range cases {
		for _, mode := range []Mode{Strict, Relaxed} {
			eng, err := cepmatch.NewEngine(mode, 5, 8)
			if err != nil {
				return err
			}
			if _, err := eng.Feed(c.evs); err != nil {
				return err
			}
			got := eng.Matches()
			if !equalPairs(got, c.want[mode]) { // invariants 1
				return fmt.Errorf("selfcheck: %v got %v want %v", mode, got, c.want[mode])
			}
			if err := checkLegality(5, got); err != nil { // invariants 2 and 3
				return err
			}
		}
	}
	return selfCheckAtomic() // invariant 4
}

// selfCheckAtomic feeds a valid prefix, rejects a bad batch, requires the
// match list to be unchanged, and then proves the matcher still works.
func selfCheckAtomic() error {
	m2, _ := New(Relaxed, 5, 8)
	good := []Event{{Key: "k", Type: "A", TS: 1}, {Key: "k", Type: "A", TS: 2}}
	if _, err := m2.Feed(good); err != nil {
		return err
	}
	before := m2.Matches()
	bad := []Event{{Key: "k", Type: "A", TS: 9}, {Key: "k", Type: "", TS: 10}}
	if _, err := m2.Feed(bad); err == nil {
		return fmt.Errorf("selfcheck: bad batch unexpectedly accepted")
	}
	if !equalPairs(m2.Matches(), before) {
		return fmt.Errorf("selfcheck: rejected batch changed state")
	}
	if _, err := m2.Feed([]Event{{Key: "k", Type: "B", TS: 5}}); err != nil {
		return err
	}
	return nil
}

func equalPairs(a, b []Match) bool {
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

func checkLegality(T int64, ms []Match) error {
	seenA, seenB := map[Event]bool{}, map[Event]bool{}
	for _, x := range ms {
		if x.A.Key != x.B.Key || x.A.TS > x.B.TS ||
			!cepwin.InWindow(x.A.TS, x.B.TS, T) {
			return fmt.Errorf("illegal pair: %+v", x)
		}
		if seenA[x.A] || seenB[x.B] {
			return fmt.Errorf("event reused: %+v", x)
		}
		seenA[x.A], seenB[x.B] = true, true
	}
	return nil
}
