// Package api is the public facade over the merge package.
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/merge"
	"ontology/msrc"
)

// Event is re-exported so callers only import api.
type Event = msrc.Event

var ErrSelfCheck = errors.New("api: self-check failed")

// System is a multi-source merge instance.
type System struct {
	mg   *merge.Merger
	once sync.Once
	log  []Event
	view map[string]string
}

func New() *System { return &System{mg: merge.New()} }

// AddSource registers a source; rejected calls leave no trace.
func (s *System) AddSource(name string, evs []Event) error { return s.mg.AddSource(name, evs) }

func (s *System) drain() {
	s.mg.Run()
	s.log = s.mg.Log()
	s.view = make(map[string]string, len(s.log))
	for _, e := range s.log { // last-write-wins, in ≺ order
		s.view[e.Key] = e.Val
	}
}

// Drain runs the merge to completion and returns the full change log.
func (s *System) Drain() []Event {
	s.once.Do(s.drain)
	return append([]Event(nil), s.log...)
}

// View returns a copy of the materialized view.
func (s *System) View() map[string]string {
	s.once.Do(s.drain)
	out := make(map[string]string, len(s.view))
	for k, v := range s.view {
		out[k] = v
	}
	return out
}

// Dups returns the number of discarded duplicate events.
func (s *System) Dups() int {
	s.once.Do(s.drain)
	return s.mg.Dups()
}

// SelfCheck verifies the four invariants on a built-in event set (the
// NOTES.md scenario). It touches no receiver state, so it is safe to call
// concurrently.
func (s *System) SelfCheck() error {
	sys := New()
	srcs := map[string][]Event{
		"A": {{Seq: 0, TS: 5, Key: "k1", Val: "a1"}, {Seq: 1, TS: 7, Key: "k2", Val: "a2"}, {Seq: 2, TS: 9, Key: "k1", Val: "a3"}},
		"B": {{Seq: 0, TS: 5, Key: "k1", Val: "b1"}, {Seq: 1, TS: 8, Key: "k3", Val: "b2"}, {Seq: 2, TS: 9, Key: "k1", Val: "b3"}},
		"C": {{Seq: 0, TS: 6, Key: "k4", Val: "c1"}, {Seq: 1, TS: 7, Key: "k2", Val: "c2"}, {Seq: 2, TS: 10, Key: "k5", Val: "c3"}},
	}
	for _, n := range []string{"A", "B", "C"} {
		if err := sys.AddSource(n, srcs[n]); err != nil {
			return fmt.Errorf("%w: add %s: %v", ErrSelfCheck, n, err)
		}
	}
	// Invariant 4: rejected operations leave no trace.
	bad := []struct {
		name string
		evs  []Event
		want error
	}{
		{"A", srcs["A"], merge.ErrDuplicateName},
		{"D", []Event{{Seq: 1, TS: 1, Key: "x"}, {Seq: 1, TS: 2, Key: "y"}}, msrc.ErrSeqNotStrictlyIncreasing},
		{"E", []Event{{Seq: 0, TS: 2, Key: "x"}, {Seq: 1, TS: 1, Key: "y"}}, msrc.ErrTSDecreasing},
		{"F", []Event{{Seq: 0, TS: 1, Key: ""}}, msrc.ErrEmptyKey},
	}
	for _, b := range bad {
		if err := sys.AddSource(b.name, b.evs); !errors.Is(err, b.want) {
			return fmt.Errorf("%w: want %v, got %v", ErrSelfCheck, b.want, err)
		}
	}
	log := sys.Drain()
	if len(log) != 6 || sys.Dups() != 3 { // invariants 2+3: 9 in, 3 dups, 6 kept
		return fmt.Errorf("%w: log=%d dups=%d", ErrSelfCheck, len(log), sys.Dups())
	}
	seen := map[[2]interface{}]bool{}
	for i, e := range log { // invariant 2: unique (Key,TS), ≺-ordered
		kt := [2]interface{}{e.Key, e.TS}
		if seen[kt] || (i > 0 && !less(log[i-1], e)) {
			return fmt.Errorf("%w: log not self-consistent at %d", ErrSelfCheck, i)
		}
		seen[kt] = true
	}
	// Invariant 1: view equals batch recompute over the kept events.
	want := map[string]string{"k1": "a3", "k2": "a2", "k3": "b2", "k4": "c1", "k5": "c3"}
	if view := sys.View(); fmt.Sprint(view) != fmt.Sprint(want) {
		return fmt.Errorf("%w: view %v != %v", ErrSelfCheck, view, want)
	}
	return nil
}

// less mirrors the ≺ order for the self-check's log-order verification.
func less(a, b Event) bool {
	if a.TS != b.TS {
		return a.TS < b.TS
	}
	if a.Src != b.Src {
		return a.Src < b.Src
	}
	return a.Seq < b.Seq
}
