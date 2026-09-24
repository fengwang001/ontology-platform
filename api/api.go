// Package api is the public entry point to the CDC before-image applier.
package api

import (
	"maps"
	"reflect"

	"ontology/rapply"
	"ontology/rimg"
)

type (
	Row      = rimg.Row
	Event    = rimg.Event
	Result   = rimg.Result
	Conflict = rimg.Conflict
)

// Sentinel errors, pairwise distinguishable. A conflict is never an error.
var (
	ErrInvalidEvent = rimg.ErrInvalidEvent
	ErrSeqGap       = rapply.ErrSeqGap
	ErrTooManyRows  = rapply.ErrTooManyRows
)

type App struct{ rep *rapply.Replica }

func New(initial map[int64]Row, maxRows int) *App { return &App{rep: rapply.New(initial, maxRows)} }

func (a *App) Apply(evs []Event) ([]Result, error) { return a.rep.Apply(evs) }

func (a *App) Snapshot() map[int64]Row { return a.rep.Snapshot() }

func (a *App) Conflicts() []Conflict { return a.rep.Conflicts() }

func (a *App) LastSeq() int64 { return a.rep.LastSeq() }

// State returns one consistent committed triple: rows, conflict log, lastSeq.
func (a *App) State() (map[int64]Row, []Conflict, int64) { return a.rep.State() }

// SelfCheck replays a built-in sequence and verifies the four invariants
// (blind-apply agreement, recomputable verdicts, conflict zero-side-effect,
// rejection leaves no trace) plus the constant read-count bound.
func (a *App) SelfCheck() bool { return checkSequence() && checkRejection() && rapply.ReadBoundOK() }

func checkSequence() bool {
	init := map[int64]Row{1: {"name": "a", "qty": "1"}, 2: {"name": "b"}}
	a := New(init, 8)
	blind := maps.Clone(init) // independent naive reference
	nConflict := 0
	for _, e := range builtinEvents() {
		before := a.Snapshot()
		res, err := a.Apply([]Event{e})
		if err != nil || len(res) != 1 {
			return false
		}
		cur, ok := before[e.PK]
		wantApplied, wantConf := false, rimg.ConflictType(0) // independent recompute
		switch {
		case e.Kind == rimg.Insert && ok:
			wantConf = rimg.RowExists
		case e.Kind == rimg.Insert:
			wantApplied = true
		case !ok:
			wantConf = rimg.RowMissing
		case !rimg.RowEqual(cur, e.Before):
			wantConf = rimg.BeforeMismatch
		default:
			wantApplied = true
		}
		if res[0].Applied != wantApplied || res[0].Conflict != wantConf {
			return false
		}
		if wantApplied {
			blindApply(blind, e) // reference advances only on applied events
			continue
		}
		nConflict++
		if !reflect.DeepEqual(a.Snapshot(), before) { // zero side effect
			return false
		}
		cf := a.Conflicts()
		if len(cf) != nConflict || cf[nConflict-1].Seq != e.Seq || cf[nConflict-1].Type != wantConf {
			return false
		}
		if nConflict > 1 && cf[nConflict-1].Seq <= cf[nConflict-2].Seq {
			return false
		}
	}
	want := map[int64]Row{1: {"name": "a", "qty": "3"}, 2: {"name": "b"}}
	return reflect.DeepEqual(a.Snapshot(), blind) && reflect.DeepEqual(a.Snapshot(), want) &&
		a.LastSeq() == 5 && nConflict == 3
}

func checkRejection() bool {
	a := New(map[int64]Row{1: {"x": "1"}}, 4)
	cases := []struct {
		evs  []Event
		want error
	}{
		{[]Event{{Seq: 1, Kind: rimg.Insert, After: Row{"": "b"}}}, ErrInvalidEvent},
		{[]Event{{Seq: 9, Kind: rimg.Insert, After: Row{"x": "2"}}}, ErrSeqGap},
	}
	for _, c := range cases {
		if !rejectUntouched(a, c.evs, c.want) {
			return false
		}
	}
	a2 := New(map[int64]Row{1: {"x": "1"}}, 1)
	if !rejectUntouched(a2, []Event{{Seq: 1, Kind: rimg.Insert, PK: 2, After: Row{"x": "2"}}}, ErrTooManyRows) {
		return false
	}
	a3 := New(map[int64]Row{}, 4) // valid first event must not survive a later reject
	batch := []Event{
		{Seq: 1, Kind: rimg.Insert, PK: 1, After: Row{"x": "1"}},
		{Seq: 2, Kind: rimg.Update, PK: 1, After: Row{"x": "2"}},
	}
	if !rejectUntouched(a3, batch, ErrInvalidEvent) {
		return false
	}
	_, err := a3.Apply([]Event{{Seq: 1, Kind: rimg.Insert, PK: 7, After: Row{"x": "9"}}})
	return err == nil // still usable after a rejected batch
}

// rejectUntouched applies evs expecting want and no state change at all.
func rejectUntouched(a *App, evs []Event, want error) bool {
	snap, conf, seq := a.Snapshot(), a.Conflicts(), a.LastSeq()
	_, err := a.Apply(evs)
	return err == want && a.LastSeq() == seq &&
		reflect.DeepEqual(a.Snapshot(), snap) && reflect.DeepEqual(a.Conflicts(), conf)
}

func builtinEvents() []Event {
	return []Event{
		{Seq: 1, Kind: rimg.Update, PK: 1, Before: Row{"name": "a", "qty": "1"}, After: Row{"name": "a", "qty": "2"}},
		{Seq: 2, Kind: rimg.Insert, PK: 2, After: Row{"name": "c"}},
		{Seq: 3, Kind: rimg.Delete, PK: 1, Before: Row{"name": "a", "qty": "9"}},
		{Seq: 4, Kind: rimg.Update, PK: 9, Before: Row{"x": "1"}, After: Row{"x": "2"}},
		{Seq: 5, Kind: rimg.Update, PK: 1, Before: Row{"name": "a", "qty": "2"}, After: Row{"name": "a", "qty": "3"}},
	}
}

func blindApply(m map[int64]Row, e Event) {
	if e.Kind == rimg.Delete {
		delete(m, e.PK)
	} else {
		m[e.PK] = maps.Clone(e.After)
	}
}
