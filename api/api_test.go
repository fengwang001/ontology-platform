package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
	"ontology/rimg"
)

func R(v ...string) rimg.Row {
	r := rimg.Row{}
	for i := 0; i < len(v); i += 2 {
		r[v[i]] = v[i+1]
	}
	return r
}
func TestRimgEqualAndShapes(t *testing.T) {
	for _, c := range []struct {
		a, b rimg.Row
		want bool
	}{
		{R("a", "1"), R("a", "1"), true},
		{R("a", "1"), R("a", "2"), false},
		{R("name", "cid"), R("name", "cid", "qty", ""), false}, // missing col != ""
		{R("x", ""), R("x", "", "y", "1"), false},
	} {
		if rimg.Equal(c.a, c.b) != c.want {
			t.Fatalf("Equal %v,%v", c.a, c.b)
		}
	}
	for _, c := range []struct {
		ev   rimg.Event
		want bool
	}{
		{rimg.Event{Kind: rimg.Insert, After: R("a", "1")}, true},
		{rimg.Event{Kind: rimg.Insert, Before: R("a", "1")}, false},
		{rimg.Event{Kind: rimg.Update, Before: R("a", "1"), After: R("a", "2")}, true},
		{rimg.Event{Kind: rimg.Update, After: R("a", "2")}, false},
		{rimg.Event{Kind: rimg.Delete, Before: R("a", "1")}, true},
		{rimg.Event{Kind: rimg.Delete, After: R("a", "1")}, false},
		{rimg.Event{Kind: rimg.Insert, After: rimg.Row{"": "x"}}, false},
		{rimg.Event{Kind: rimg.Insert, After: rimg.Row{}}, false},
	} {
		if rimg.ValidEvent(c.ev) != c.want {
			t.Fatalf("ValidEvent %+v", c.ev)
		}
	}
}
func TestEightStepScenario(t *testing.T) {
	seed := map[int64]rimg.Row{1: R("name", "ann", "qty", "3"), 2: R("name", "bob", "qty", "5"), 3: R("name", "cid")}
	a, _ := api.New(seed, 4)
	U, D, I := rimg.Update, rimg.Delete, rimg.Insert
	evs := []rimg.Event{
		{Seq: 1, Kind: U, PK: 1, Before: R("name", "ann", "qty", "3"), After: R("name", "ann", "qty", "4")},
		{Seq: 2, Kind: U, PK: 2, Before: R("name", "bob", "qty", "6"), After: R("name", "bob", "qty", "7")},
		{Seq: 3, Kind: D, PK: 3, Before: R("name", "cid", "qty", "")},
		{Seq: 4, Kind: I, PK: 2, After: R("name", "bea", "qty", "1")},
		{Seq: 5, Kind: D, PK: 1, Before: R("name", "ann", "qty", "3")},
		{Seq: 6, Kind: U, PK: 4, Before: R("name", "dan"), After: R("name", "dan", "qty", "2")},
		{Seq: 7, Kind: I, PK: 4, After: R("name", "dan", "qty", "2")},
		{Seq: 8, Kind: U, PK: 4, Before: R("name", "dan", "qty", "2"), After: R("name", "dan", "qty", "3")},
	}
	want := []int{int(api.Applied), int(api.BeforeMismatch), int(api.BeforeMismatch), int(api.RowExists),
		int(api.BeforeMismatch), int(api.RowMissing), int(api.Applied), int(api.Applied)}
	for i, ev := range evs {
		res, err := a.Apply([]rimg.Event{ev})
		if err != nil || int(res[0].Outcome) != want[i] {
			t.Fatalf("step %d: %+v %v", i+1, res, err)
		}
	}
	s := a.Snapshot()
	if s.LastSeq != 8 || len(s.Rows) != 4 || s.Rows[1]["qty"] != "4" || s.Rows[2]["qty"] != "5" ||
		len(s.Rows[3]) != 1 || s.Rows[4]["qty"] != "3" || len(s.Conflicts) != 5 {
		t.Fatalf("bad final: %+v", s)
	}
}
func TestConflictClassification(t *testing.T) {
	a, _ := api.New(map[int64]rimg.Row{1: R("v", "a")}, 10)
	for _, c := range []struct {
		ev   rimg.Event
		want int
	}{
		{rimg.Event{Seq: 1, Kind: rimg.Insert, PK: 1, After: R("v", "b")}, int(api.RowExists)},
		{rimg.Event{Seq: 2, Kind: rimg.Update, PK: 9, Before: R("v", "x"), After: R("v", "y")}, int(api.RowMissing)},
		{rimg.Event{Seq: 3, Kind: rimg.Delete, PK: 9, Before: R("v", "x")}, int(api.RowMissing)},
		{rimg.Event{Seq: 4, Kind: rimg.Update, PK: 1, Before: R("v", "z"), After: R("v", "y")}, int(api.BeforeMismatch)},
		{rimg.Event{Seq: 5, Kind: rimg.Update, PK: 1, Before: R("v", "a"), After: R("v", "y")}, int(api.Applied)},
	} {
		res, err := a.Apply([]rimg.Event{c.ev})
		if err != nil || int(res[0].Outcome) != c.want {
			t.Fatalf("%+v: %+v %v", c.ev, res, err)
		}
	}
	if len(a.Conflicts()) != 4 {
		t.Fatal("conflicts must be logged, not errored")
	}
}
func TestSentinelsDistinctAndRecoverable(t *testing.T) {
	if errors.Is(api.ErrInvalidEvent, api.ErrSeqGap) || errors.Is(api.ErrSeqGap, api.ErrTooManyRows) ||
		errors.Is(api.ErrInvalidEvent, api.ErrTooManyRows) {
		t.Fatal("sentinels not distinct")
	}
	a, _ := api.New(map[int64]rimg.Row{1: R("v", "a")}, 1)
	before := a.Snapshot()
	reject := func(ev rimg.Event, want error) {
		if _, err := a.Apply([]rimg.Event{ev}); !errors.Is(err, want) {
			t.Fatalf("want %v got %v", want, err)
		}
	}
	reject(rimg.Event{Seq: 1, Kind: rimg.Insert, PK: 1, Before: R("v", "a")}, api.ErrInvalidEvent)
	reject(rimg.Event{Seq: 7, Kind: rimg.Insert, PK: 2, After: R("v", "b")}, api.ErrSeqGap)
	reject(rimg.Event{Seq: 1, Kind: rimg.Insert, PK: 2, After: R("v", "b")}, api.ErrTooManyRows)
	after := a.Snapshot()
	if after.LastSeq != before.LastSeq || len(after.Rows) != 1 || len(after.Conflicts) != 0 {
		t.Fatal("rejections changed state")
	}
	if _, err := a.Apply([]rimg.Event{{Seq: 1, Kind: rimg.Update, PK: 1, Before: R("v", "a"), After: R("v", "b")}}); err != nil {
		t.Fatalf("not reusable: %v", err)
	}
}
func TestConcurrentBatchBoundary(t *testing.T) {
	const batches, readers = 400, 8
	a, _ := api.New(map[int64]rimg.Row{}, 1<<20)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < readers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 2000; i++ { // bounded spins, no sleep; boundary k: rows==seq
				s := a.Snapshot()
				if len(s.Rows) != int(s.LastSeq) || len(s.Conflicts) != 0 {
					t.Errorf("torn batch rows=%d seq=%d", len(s.Rows), s.LastSeq)
					return
				}
			}
		}()
	}
	close(start)
	for k := int64(1); k <= batches; k++ {
		_, _ = a.Apply([]rimg.Event{{Seq: k, Kind: rimg.Insert, PK: k, After: R("v", "x")}})
	}
	wg.Wait()
	if s := a.Snapshot(); s.LastSeq != batches || len(s.Rows) != batches {
		t.Fatalf("final %+v", s)
	}
}
