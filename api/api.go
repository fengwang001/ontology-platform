// Package api is the external surface of the CDC merge/dedup pipeline.
// It depends only on merge.
package api

import (
	"errors"
	"fmt"
	"sort"

	"ontology/merge"
	"ontology/msrc"
)

// ErrSelfCheckFailed wraps the concrete reason when SelfCheck finds a broken
// invariant; callers judge it with errors.Is.
var ErrSelfCheckFailed = errors.New("api: self-check failed")

// Event re-exports the underlying event type so callers need not import msrc.
type Event = msrc.Event

// Pipeline is the public facade.
type Pipeline struct {
	m *merge.Merger
}

// New creates an empty pipeline.
func New() *Pipeline { return &Pipeline{m: merge.New()} }

// AddSource validates and registers a source. A rejected source (a msrc
// sentinel error or merge.ErrDupSource) has no effect on any state.
func (p *Pipeline) AddSource(name string, evs []Event) error { return p.m.Add(name, evs) }

// Drain returns the complete change log (retained events in ≺ order).
func (p *Pipeline) Drain() []Event { return p.m.Drain() }

// View returns the materialized view after replaying the change log LWW.
func (p *Pipeline) View() map[string]string { return p.m.View() }

// Dups returns the total number of dropped duplicate events.
func (p *Pipeline) Dups() int { return p.m.Dups() }

// SelfCheck builds a fixed set of built-in source sequences and verifies all
// four invariants, including that rejected registration leaves no trace.
// It returns nil on success or an error wrapping ErrSelfCheckFailed.
func (p *Pipeline) SelfCheck() error {
	q := New()
	srcA := []Event{
		{Src: "A", Seq: 0, TS: 5, Key: "k1", Val: "a1"},
		{Src: "A", Seq: 1, TS: 7, Key: "k2", Val: "a2"},
		{Src: "A", Seq: 2, TS: 9, Key: "k1", Val: "a3"},
	}
	srcB := []Event{
		{Src: "B", Seq: 0, TS: 5, Key: "k1", Val: "b1"},
		{Src: "B", Seq: 1, TS: 8, Key: "k3", Val: "b2"},
		{Src: "B", Seq: 2, TS: 9, Key: "k1", Val: "b3"},
	}
	srcC := []Event{
		{Src: "C", Seq: 0, TS: 6, Key: "k4", Val: "c1"},
		{Src: "C", Seq: 1, TS: 7, Key: "k2", Val: "c2"},
		{Src: "C", Seq: 2, TS: 10, Key: "k5", Val: "c3"},
	}
	for _, t := range []struct {
		name string
		evs  []Event
	}{{"A", srcA}, {"B", srcB}, {"C", srcC}} {
		if err := q.AddSource(t.name, t.evs); err != nil {
			return err
		}
	}
	builtin := append(append(append([]Event{}, srcA...), srcB...), srcC...)

	log := q.Drain()
	// Independent batch recompute over the retained events (invariant 1):
	// sort the full built-in input by ≺, keep the ≺-minimum per (Key,TS),
	// then apply last-write-wins.
	in := append([]Event(nil), builtin...)
	sort.Slice(in, func(i, j int) bool { return less(in[i], in[j]) })
	win := map[[2]any]Event{}
	for _, e := range in {
		k := [2]any{e.Key, e.TS}
		if w, ok := win[k]; !ok || less(e, w) {
			win[k] = e
		}
	}
	batch := map[string]string{}
	for _, e := range in { // winners applied in ≺ order: correct last-write-wins
		if win[[2]any{e.Key, e.TS}] == e {
			batch[e.Key] = e.Val
		}
	}
	if !equalMap(q.View(), batch) {
		return fmt.Errorf("%w: view differs from batch recompute", ErrSelfCheckFailed)
	}
	seen := map[[2]any]bool{}
	for _, e := range log { // invariant 2: unique (Key,TS) in change log
		k := [2]any{e.Key, e.TS}
		if seen[k] {
			return fmt.Errorf("%w: duplicate (key,ts) in change log", ErrSelfCheckFailed)
		}
		seen[k] = true
	}
	if q.Dups() != 3 || len(log) != 6 { // invariant 3: exactly 3 dropped, 6 kept
		return fmt.Errorf("%w: dedup count wrong", ErrSelfCheckFailed)
	}

	// Invariant 4: rejected registrations leave no trace.
	before := q.Drain()
	bad := []Event{{Seq: 0, TS: 1, Key: ""}, {Seq: 1, TS: 1, Key: "z"}}
	if err := q.AddSource("D", bad); !errors.Is(err, msrc.ErrEmptyKey) {
		return fmt.Errorf("%w: invalid source not rejected", ErrSelfCheckFailed)
	}
	if err := q.AddSource("A", nil); !errors.Is(err, merge.ErrDupSource) {
		return fmt.Errorf("%w: duplicate source name not rejected", ErrSelfCheckFailed)
	}
	after := q.Drain()
	if len(after) != len(before) || q.Dups() != 3 {
		return fmt.Errorf("%w: rejected add changed state", ErrSelfCheckFailed)
	}
	return nil
}

// less is the independent global total order ≺ used only by SelfCheck.
func less(a, b Event) bool {
	if a.TS != b.TS {
		return a.TS < b.TS
	}
	if a.Src != b.Src {
		return a.Src < b.Src
	}
	return a.Seq < b.Seq
}

func equalMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
