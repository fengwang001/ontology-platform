package api

import (
	"errors"
	"maps"
	"math/rand"

	"ontology/rapply"
	"ontology/rimg"
)

type (
	Row      = rimg.Row
	Event    = rimg.Event
	Kind     = rimg.Kind
	Result   = rapply.Result
	Conflict = rapply.Conflict
)
type State struct {
	Rows      map[int64]Row
	Conflicts []Conflict
	LastSeq   int64
}

const Insert, Update, Delete, Applied, RowExists, RowMissing, BeforeMismatch = rimg.Insert, rimg.Update, rimg.Delete, rapply.Applied, rapply.RowExists, rapply.RowMissing, rapply.BeforeMismatch

var ErrInvalidEvent, ErrSeqGap, ErrTooManyRows = rapply.ErrInvalidEvent, rapply.ErrSeqGap, rapply.ErrTooManyRows

type App struct{ e *rapply.Engine }

func New(initial map[int64]Row, maxRows int) (a *App, err error) {
	e, err := rapply.New(initial, maxRows)
	if err == nil {
		a = &App{e: e}
	}
	return
}
func (a *App) Apply(evs []Event) ([]Result, error) { return a.e.ApplyBatch(evs) }
func (a *App) Snapshot() State {
	r, cf, seq := a.e.Snapshot()
	return State{Rows: r, Conflicts: cf, LastSeq: seq}
}
func (a *App) Conflicts() []Conflict { return a.e.Conflicts() }
func eq(x, y map[int64]Row) bool {
	if len(x) != len(y) {
		return false
	}
	for pk, r := range x {
		if s, ok := y[pk]; !ok || !rimg.Equal(r, s) {
			return false
		}
	}
	return true
}
func genEvents(n int) []Event {
	rnd := rand.New(rand.NewSource(271))
	evs := make([]Event, n)
	for i := range evs {
		ev := Event{Seq: int64(i + 1), Kind: Kind(rnd.Intn(3) + 1), PK: int64(rnd.Intn(8)) + 1}
		v := Row{"v": string(rune('a' + rnd.Intn(4)))}
		if ev.Kind == rimg.Insert {
			ev.After = v
		} else if ev.Kind == rimg.Update {
			ev.Before, ev.After = Row{"v": "a"}, v
		} else {
			ev.Before = Row{"v": "a"}
		}
		evs[i] = ev
	}
	return evs
}
func SelfCheck() error {
	seed := map[int64]Row{1: {"v": "a"}, 2: {"v": "a"}, 3: {"v": "a"}}
	a, _ := New(seed, 1<<20)
	model := maps.Clone(seed)
	var nCf int
	for _, ev := range genEvents(400) {
		pre := a.Snapshot()
		res, err := a.Apply([]Event{ev})
		if err != nil {
			return err
		}
		cur, ok := model[ev.PK]
		want := rapply.Applied // I2: independent first-rule recompute.
		if ev.Kind == rimg.Insert {
			if ok {
				want = rapply.RowExists
			}
		} else if !ok {
			want = rapply.RowMissing
		} else if !rimg.Equal(cur, ev.Before) {
			want = rapply.BeforeMismatch
		}
		if res[0].Outcome != want {
			return errors.New("selfcheck: ordered-rule mismatch")
		}
		post := a.Snapshot()
		if want != rapply.Applied { // I3: conflict leaves rows identical.
			nCf++
			if !eq(pre.Rows, post.Rows) {
				return errors.New("selfcheck: conflict mutated rows")
			}
		} else if ev.Kind == rimg.Delete { // I1: blind applied-only model.
			delete(model, ev.PK)
		} else {
			model[ev.PK] = maps.Clone(ev.After)
		}
		if !eq(model, post.Rows) {
			return errors.New("selfcheck: blind-reference drift")
		}
	}
	log := a.Conflicts()
	if len(log) != nCf {
		return errors.New("selfcheck: conflict count")
	}
	for i := 1; i < len(log); i++ {
		if log[i].Seq <= log[i-1].Seq {
			return errors.New("selfcheck: conflict seq not increasing")
		}
	}
	return rejectionsAtomic() // I4
}
func rejectionsAtomic() error {
	seed := map[int64]Row{1: {"v": "a"}}
	cases := []struct {
		batch []Event
		want  error
	}{
		{[]Event{{Seq: 1, Kind: Update, PK: 1, After: Row{"v": "b"}}}, ErrInvalidEvent},
		{[]Event{{Seq: 9, Kind: Insert, PK: 2, After: Row{"v": "b"}}}, ErrSeqGap},
		{[]Event{{Seq: 1, Kind: Insert, PK: 2, After: Row{"v": "b"}}}, ErrTooManyRows},
	}
	for _, c := range cases {
		a, _ := New(seed, 1)
		s0 := a.Snapshot()
		if _, err := a.Apply(c.batch); !errors.Is(err, c.want) {
			return errors.New("selfcheck: expected sentinel")
		}
		s1 := a.Snapshot()
		if s1.LastSeq != s0.LastSeq || len(s1.Conflicts) != len(s0.Conflicts) || !eq(s0.Rows, s1.Rows) {
			return errors.New("selfcheck: rejection left a trace")
		}
		good := Event{Seq: 1, Kind: Update, PK: 1, Before: Row{"v": "a"}, After: Row{"v": "b"}}
		if _, err := a.Apply([]Event{good}); err != nil {
			return errors.New("selfcheck: unusable after reject")
		}
	}
	return nil
}
