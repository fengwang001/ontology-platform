// Package api is the public face of the per-key debounce refresher.
// It depends only on thr.
package api

import (
	"errors"
	"reflect"

	"ontology/thr"
)

// Refresh is one fired refresh record (key, merged count, latest value).
type Refresh = thr.Refresh

// Refresher debounces upstream changes per key into a materialized view.
type Refresher struct{ t *thr.Throttler }

// New creates a refresher; w and maxKeys must be positive.
func New(w, maxKeys int64) (*Refresher, error) {
	t, err := thr.New(w, maxKeys)
	if err != nil {
		return nil, err
	}
	return &Refresher{t: t}, nil
}

// Record accepts one change, merging it into the key's open batch.
func (r *Refresher) Record(key string, at int64, val string) error {
	return r.t.Record(key, at, val)
}

// Tick fires all batches due at or before now and returns them.
func (r *Refresher) Tick(now int64) []Refresh { return r.t.Tick(now) }

// Stop flushes every pending batch regardless of due time.
func (r *Refresher) Stop(now int64) []Refresh { return r.t.Stop(now) }

// View returns a snapshot copy of the materialized view.
func (r *Refresher) View() map[string]string { return r.t.View() }

// Fired reports the cumulative number of fired batches.
func (r *Refresher) Fired() int { return int(r.t.Fired()) }

// SelfCheck replays a built-in sequence on a fresh internal instance and
// verifies the four invariants; it never mutates the receiver.
func (r *Refresher) SelfCheck() error {
	h, err := New(3, 16)
	if err != nil {
		return err
	}
	fail := func(m string) error { return errors.New("debounce selfcheck: " + m) }
	type ev struct {
		k string
		t int64
		v string
	}
	feed := func(e ev) {
		if e := h.Record(e.k, e.t, e.v); e != nil {
			err = e
		}
	}
	all := []ev{{"k", 0, "a"}, {"k", 2, "b"}, {"m", 2, "x"}}
	for _, e := range all {
		feed(e)
	}
	if err != nil {
		return err
	}
	if len(h.Tick(4)) != 0 {
		return fail("step4 must fire nothing")
	}
	feed(ev{"k", 5, "c"}) // step5: t == old Due(5): still merges
	if err != nil {
		return err
	}
	if got := h.Tick(5); !reflect.DeepEqual(got, []Refresh{{Key: "m", N: 1, Val: "x"}}) {
		return fail("step6 must fire only (m,1,x) with equality boundary")
	}
	feed(ev{"m", 5, "y"})
	feed(ev{"z", 6, "p"})
	feed(ev{"z", 6, "q"}) // equal-t tie: later arrival wins
	if err != nil {
		return err
	}
	all = append(all, ev{"k", 5, "c"}, ev{"m", 5, "y"}, ev{"z", 6, "p"}, ev{"z", 6, "q"})
	h.Stop(8)

	// Invariant 1: final view == naive max-t (later-wins) reference.
	ref := map[string]string{}
	for _, e := range all {
		ref[e.k] = e.v
	}
	if !reflect.DeepEqual(h.View(), ref) {
		return fail("invariant1 final value equivalence")
	}
	// Invariant 2: fired batches <= accepted changes; no fabricated values.
	if h.Fired() > len(all) {
		return fail("invariant2 fired exceeds accepted changes")
	}
	vals := map[string]map[string]bool{}
	for _, e := range all {
		if vals[e.k] == nil {
			vals[e.k] = map[string]bool{}
		}
		vals[e.k][e.v] = true
	}
	for k, v := range h.View() {
		if !vals[k][v] {
			return fail("invariant2 fabricated view value")
		}
	}
	// Invariant 3 + 4: rejected ops leave no trace and stay decidable.
	f0, v0 := h.Fired(), h.View()
	cases := []func() error{
		func() error { _, e := New(0, 1); return e },
		func() error { _, e := New(1, -1); return e },
		func() error { return h.Record("", 9, "z") },
		func() error { return h.Record("k", 7, "back") },
	}
	want := []error{thr.ErrBadParam, thr.ErrBadParam, thr.ErrEmptyKey, thr.ErrClockBack}
	for i, c := range cases {
		if !errors.Is(c(), want[i]) {
			return fail("decidable error mismatch")
		}
	}
	// a backward Tick is rejected by firing nothing (Tick has no error return)
	if h.Tick(7) != nil {
		return fail("backward Tick must fire nothing")
	}
	if h.Fired() != f0 || !reflect.DeepEqual(h.View(), v0) {
		return fail("invariant3/4 rejection changed state")
	}
	small, e := New(3, 1)
	if e != nil {
		return e
	}
	if e := small.Record("a", 0, "1"); e != nil {
		return e
	}
	if !errors.Is(small.Record("b", 0, "2"), thr.ErrTooMany) {
		return fail("missing ErrTooMany")
	}
	if e := h.Record("n", 9, "v"); e != nil || h.Stop(9) == nil {
		return fail("instance unusable after rejections")
	}
	return nil
}
