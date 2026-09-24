package api_test

import (
	"math/rand/v2"
	"reflect"
	"testing"

	"ontology/api"
	"ontology/rimg"
)

type E = rimg.Event
type R = rimg.Row

func row(s ...string) (r R) {
	r = R{}
	for i := 0; i+1 < len(s); i += 2 {
		r[s[i]] = s[i+1]
	}
	return
}
func rndE(seq int64, rng *rand.Rand) E {
	e := E{Seq: seq, Kind: rimg.Kind(1 + rng.IntN(3)), PK: int64(rng.IntN(12)), Before: row("c", string(rune('a'+rng.IntN(4)))), After: row("c", string(rune('a'+rng.IntN(4))))}
	if e.Kind == rimg.Insert {
		e.Before = nil
	}
	if e.Kind == rimg.Delete {
		e.After = nil
	}
	return e
}
func TestEightEvents(t *testing.T) { // section-3 step table, invariant 2
	a := api.New(map[int64]R{1: row("name", "ann", "qty", "3"), 2: row("name", "bob", "qty", "5"), 3: row("name", "cid")}, 4)
	evs := []E{
		{Seq: 1, Kind: rimg.Update, PK: 1, Before: row("name", "ann", "qty", "3"), After: row("name", "ann", "qty", "4")},
		{Seq: 2, Kind: rimg.Update, PK: 2, Before: row("name", "bob", "qty", "6"), After: row("name", "bob", "qty", "7")},
		{Seq: 3, Kind: rimg.Delete, PK: 3, Before: row("name", "cid", "qty", "")},
		{Seq: 4, Kind: rimg.Insert, PK: 2, After: row("name", "bea", "qty", "1")},
		{Seq: 5, Kind: rimg.Delete, PK: 1, Before: row("name", "ann", "qty", "3")},
		{Seq: 6, Kind: rimg.Update, PK: 4, Before: row("name", "dan"), After: row("name", "dan", "qty", "2")},
		{Seq: 7, Kind: rimg.Insert, PK: 4, After: row("name", "dan", "qty", "2")},
		{Seq: 8, Kind: rimg.Update, PK: 4, Before: row("name", "dan", "qty", "2"), After: row("name", "dan", "qty", "3")},
	}
	want := []rimg.ConflictType{0, rimg.BeforeMismatch, rimg.BeforeMismatch, rimg.RowExists, rimg.BeforeMismatch, rimg.RowMissing, 0, 0}
	for i, e := range evs {
		res, err := a.Apply([]E{e})
		if err != nil || res[0].Conflict != want[i] || res[0].Applied != (want[i] == 0) {
			t.Fatalf("step %d: %v %v", i+1, res, err)
		}
	}
	fin := map[int64]R{1: row("name", "ann", "qty", "4"), 2: row("name", "bob", "qty", "5"), 3: row("name", "cid"), 4: row("name", "dan", "qty", "3")}
	wantC := []rimg.Conflict{{Seq: 2, PK: 2, Type: rimg.BeforeMismatch}, {Seq: 3, PK: 3, Type: rimg.BeforeMismatch}, {Seq: 4, PK: 2, Type: rimg.RowExists}, {Seq: 5, PK: 1, Type: rimg.BeforeMismatch}, {Seq: 6, PK: 4, Type: rimg.RowMissing}}
	if !reflect.DeepEqual(a.Snapshot(), fin) || !reflect.DeepEqual(a.Conflicts(), wantC) || a.LastSeq() != 8 {
		t.Fatalf("final=%v conf=%v", a.Snapshot(), a.Conflicts())
	}
}
func TestBlindApplyRandom(t *testing.T) { // invariant 1 vs naive blind reference
	rng := rand.New(rand.NewPCG(3, 4))
	a, ref := api.New(map[int64]R{0: row("c", "0")}, 100), map[int64]R{0: row("c", "0")}
	seq := int64(1)
	for range 100 {
		evs := make([]E, 1+rng.IntN(5))
		for i := range evs {
			evs[i] = rndE(seq, rng)
			seq++
		}
		res, err := a.Apply(evs)
		if err != nil {
			t.Fatal(err)
		}
		for j, e := range evs {
			if res[j].Applied && e.Kind == rimg.Delete {
				delete(ref, e.PK)
			}
			if res[j].Applied && e.Kind != rimg.Delete {
				ref[e.PK] = e.After
			}
		}
		if !reflect.DeepEqual(a.Snapshot(), ref) {
			t.Fatal("replica diverged from blind reference")
		}
	}
}
func TestConflictZeroSideEffect(t *testing.T) { // invariant 3, all three types
	a := api.New(map[int64]R{1: row("a", "1")}, 10)
	cases := []struct {
		e    E
		want rimg.ConflictType
	}{
		{E{Seq: 1, Kind: rimg.Insert, PK: 1, After: row("a", "2")}, rimg.RowExists},
		{E{Seq: 2, Kind: rimg.Update, PK: 2, Before: row("a", "0"), After: row("a", "9")}, rimg.RowMissing},
		{E{Seq: 3, Kind: rimg.Update, PK: 1, Before: row("a", "9"), After: row("a", "8")}, rimg.BeforeMismatch},
	}
	for i, c := range cases {
		before := a.Snapshot()
		res, err := a.Apply([]E{c.e})
		cf := a.Conflicts()
		ok := err == nil && len(res) == 1 && !res[0].Applied && res[0].Conflict == c.want &&
			reflect.DeepEqual(a.Snapshot(), before) && len(cf) == i+1 && cf[i].Seq == c.e.Seq && cf[i].Type == c.want
		if !ok || (i > 0 && cf[i].Seq <= cf[i-1].Seq) {
			t.Fatalf("case %d", i)
		}
	}
}
func TestAtomicReject(t *testing.T) { // invariant 4: no trace, then usable
	cases := []struct {
		init    map[int64]R
		maxRows int
		evs     []E
		want    error
	}{
		{map[int64]R{1: row("x", "1")}, 4, []E{{Seq: 1, Kind: rimg.Insert, After: row("", "z")}}, api.ErrInvalidEvent},
		{map[int64]R{1: row("x", "1")}, 4, []E{{Seq: 9, Kind: rimg.Insert, PK: 2, After: row("x", "2")}}, api.ErrSeqGap},
		{map[int64]R{1: row("x", "1")}, 1, []E{{Seq: 1, Kind: rimg.Insert, PK: 2, After: row("x", "2")}}, api.ErrTooManyRows},
		{map[int64]R{}, 4, []E{{Seq: 1, Kind: rimg.Insert, PK: 1, After: row("x", "1")}, {Seq: 2, Kind: rimg.Update, PK: 1}}, api.ErrInvalidEvent},
	}
	for i, c := range cases {
		a := api.New(c.init, c.maxRows)
		rows, conf, seq := a.Snapshot(), a.Conflicts(), a.LastSeq()
		_, err := a.Apply(c.evs)
		ok := err == c.want && a.LastSeq() == seq && reflect.DeepEqual(a.Snapshot(), rows) && reflect.DeepEqual(a.Conflicts(), conf)
		_, err2 := a.Apply([]E{{Seq: seq + 1, Kind: rimg.Delete, PK: 77, Before: row("x", "9")}})
		if !ok || err2 != nil {
			t.Fatalf("case %d: %v %v", i, err, err2)
		}
	}
}
func TestSentinelErrors(t *testing.T) { // three errors distinct; conflict != error
	distinct := api.ErrInvalidEvent != api.ErrSeqGap && api.ErrSeqGap != api.ErrTooManyRows && api.ErrInvalidEvent != api.ErrTooManyRows
	a := api.New(map[int64]R{1: row("a", "1")}, 4)
	res, err := a.Apply([]E{{Seq: 1, Kind: rimg.Insert, PK: 1, After: row("a", "2")}})
	if !distinct || err != nil || res[0].Applied || res[0].Conflict != rimg.RowExists {
		t.Fatal("sentinels not distinct, or conflict surfaced as error")
	}
}
func TestSelfCheck(t *testing.T) {
	if !api.New(map[int64]R{1: row("a", "1")}, 8).SelfCheck() {
		t.Fatal("SelfCheck failed")
	}
}
