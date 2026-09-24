package rapply

import (
	"errors"
	"maps"
	"math/rand"
	"reflect"
	"testing"

	"ontology/rimg"
)

func rw(v string) rimg.Row { return rimg.Row{"v": v} }
func newE() *Engine        { e, _ := New(map[int64]rimg.Row{1: rw("a")}, 1<<20); return e }
func blind(m map[int64]rimg.Row, ev rimg.Event) {
	if ev.Kind == rimg.Delete {
		delete(m, ev.PK)
	} else {
		m[ev.PK] = maps.Clone(ev.After)
	}
}
func randEvents(n int) []rimg.Event {
	rnd := rand.New(rand.NewSource(7))
	evs := make([]rimg.Event, n)
	for i := range evs {
		v := rw(string(rune('a' + rnd.Intn(4))))
		evs[i] = rimg.Event{Seq: int64(i + 1), Kind: rimg.Kind(rnd.Intn(3) + 1), PK: int64(rnd.Intn(6)) + 1}
		switch evs[i].Kind {
		case rimg.Insert:
			evs[i].After = v
		case rimg.Update:
			evs[i].Before, evs[i].After = v, v
		default:
			evs[i].Before = v
		}
	}
	return evs
}
func recompute(m map[int64]rimg.Row, ev rimg.Event) Outcome {
	cur, ok := m[ev.PK]
	switch {
	case ev.Kind == rimg.Insert && ok:
		return RowExists
	case ev.Kind != rimg.Insert && !ok:
		return RowMissing
	case ev.Kind != rimg.Insert && !rimg.Equal(cur, ev.Before):
		return BeforeMismatch
	}
	return Applied
}
func TestRandomBlindConsistency(t *testing.T) {
	e, model := newE(), map[int64]rimg.Row{1: rw("a")}
	for _, ev := range randEvents(400) {
		res, err := e.ApplyBatch([]rimg.Event{ev})
		if err != nil {
			t.Fatal(err)
		}
		if res[0].Outcome == Applied {
			blind(model, ev)
		}
		rows, _, _ := e.Snapshot()
		if !reflect.DeepEqual(rows, model) {
			t.Fatalf("seq %d drifted from blind model", ev.Seq)
		}
	}
}
func TestRecomputeOrderedRules(t *testing.T) {
	e, model := newE(), map[int64]rimg.Row{1: rw("a")}
	for _, ev := range randEvents(400) {
		res, _ := e.ApplyBatch([]rimg.Event{ev})
		if res[0].Outcome != recompute(model, ev) {
			t.Fatalf("seq %d outcome %d not recomputable", ev.Seq, res[0].Outcome)
		}
		if res[0].Outcome == Applied {
			blind(model, ev)
		}
	}
}
func TestConflictZeroSideEffect(t *testing.T) {
	e := newE()
	for _, ev := range randEvents(300) {
		before, _, _ := e.Snapshot()
		res, _ := e.ApplyBatch([]rimg.Event{ev})
		after, _, _ := e.Snapshot()
		if res[0].Outcome != Applied && !reflect.DeepEqual(before, after) {
			t.Fatalf("conflict seq %d mutated rows", ev.Seq)
		}
	}
}
func TestConflictLogCompleteStrict(t *testing.T) {
	e := newE()
	n := 0
	for _, ev := range randEvents(400) {
		if res, _ := e.ApplyBatch([]rimg.Event{ev}); res[0].Outcome != Applied {
			n++
		}
	}
	log := e.Conflicts()
	if len(log) != n || n == 0 {
		t.Fatalf("log=%d conflicts=%d", len(log), n)
	}
	for i := 1; i < len(log); i++ {
		if log[i].Seq <= log[i-1].Seq {
			t.Fatalf("seq not strictly increasing at %d", i)
		}
	}
}
func TestRejectedBatchAtomic(t *testing.T) {
	bad := []struct {
		batch []rimg.Event
		want  error
	}{
		{[]rimg.Event{{Seq: 2, Kind: rimg.Update, PK: 1, After: rw("y")}}, ErrInvalidEvent},
		{[]rimg.Event{{Seq: 2, Kind: rimg.Insert, PK: 9, After: rw("x")}, {Seq: 6, Kind: rimg.Insert, PK: 10, After: rw("y")}}, ErrSeqGap},
		{[]rimg.Event{{Seq: 2, Kind: rimg.Insert, PK: 2, After: rw("x")}, {Seq: 3, Kind: rimg.Insert, PK: 3, After: rw("y")}}, ErrTooManyRows},
	}
	for _, c := range bad {
		e, _ := New(map[int64]rimg.Row{1: rw("a")}, 1)
		_, _ = e.ApplyBatch([]rimg.Event{{Seq: 1, Kind: rimg.Insert, PK: 1, After: rw("z")}})
		r0, c0, s0 := e.Snapshot()
		if _, err := e.ApplyBatch(c.batch); !errors.Is(err, c.want) {
			t.Fatal(err)
		}
		r1, c1, s1 := e.Snapshot()
		if s1 != s0 || len(c1) != len(c0) || !reflect.DeepEqual(r0, r1) {
			t.Fatal("rejected batch left a trace")
		}
		_, _ = e.ApplyBatch([]rimg.Event{{Seq: 2, Kind: rimg.Update, PK: 1, Before: rw("a"), After: rw("b")}})
	}
}
func TestLookupReadCountConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		init := make(map[int64]rimg.Row, m)
		for i := range m {
			init[int64(i)+1] = rimg.Row{"c": "v"}
		}
		e, _ := New(init, m+1)
		for _, ev := range []rimg.Event{
			{Seq: 1, Kind: rimg.Update, PK: 1, Before: rimg.Row{"c": "v"}, After: rimg.Row{"c": "w"}},
			{Seq: 2, Kind: rimg.Delete, PK: 1, Before: rimg.Row{"c": "x"}},
			{Seq: 3, Kind: rimg.Insert, PK: 1, After: rimg.Row{"c": "z"}},
		} {
			_, _ = e.ApplyBatch([]rimg.Event{ev})
			if e.reads > 2 {
				t.Fatalf("m=%d reads=%d grew with size", m, e.reads)
			}
		}
	}
}
