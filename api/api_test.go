package api_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/api"
	"ontology/pred"
)

func baseRows() []api.Row {
	d := [][3]any{
		{"east", 50, 2020}, {"east", 120, 2020}, {"east", 200, 2021}, {"west", 80, 2020},
		{"west", 150, 2021}, {"north", 100, 2021}, {"east", 90, 2022}, {"west", 300, 2022},
	}
	rs := []api.Row{}
	for _, x := range d {
		rs = append(rs, api.Row{"region": x[0], "amount": x[1], "year": x[2]})
	}
	return rs
}
func newEngine(t *testing.T) *api.Engine {
	t.Helper()
	e, err := api.New(baseRows())
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.RegisterFilter("V1", pred.Pred{Region: pred.RegionEq("east")}, []string{"region", "amount"}))
	must(e.RegisterFilter("V2", pred.Pred{Amount: pred.IGe(100)}, []string{"region", "amount", "year"}))
	must(e.RegisterAgg("V3", pred.Pred{}, []string{"region"}))
	return e
}
func sameRows(a, b []api.Row) bool { return fmt.Sprint(a) == fmt.Sprint(b) }

func TestSevenQueries(t *testing.T) {
	e := newEngine(t)
	EA, RA := pred.Pred{Region: pred.RegionEq("east")}, []string{"region", "amount"}
	cs := []struct {
		p  pred.Pred
		pj []string
		ag *api.AggSpec
		w  []api.Row
	}{
		{EA, RA, nil, []api.Row{{"region": "east", "amount": 50}, {"region": "east", "amount": 90},
			{"region": "east", "amount": 120}, {"region": "east", "amount": 200}}},
		{pred.Pred{Region: pred.RegionEq("east"), Amount: pred.IGe(100)}, RA, nil,
			[]api.Row{{"region": "east", "amount": 120}, {"region": "east", "amount": 200}}},
		{pred.Pred{Amount: pred.IGt(100)}, RA, nil, []api.Row{
			{"region": "east", "amount": 120}, {"region": "east", "amount": 200},
			{"region": "west", "amount": 150}, {"region": "west", "amount": 300}}},
		{EA, []string{"region", "amount", "year"}, nil, []api.Row{
			{"region": "east", "amount": 50, "year": 2020}, {"region": "east", "amount": 120, "year": 2020},
			{"region": "east", "amount": 200, "year": 2021}, {"region": "east", "amount": 90, "year": 2022}}},
		{pred.Pred{Region: pred.RegionEq("west"), Amount: pred.IGe(200)}, RA, nil,
			[]api.Row{{"region": "west", "amount": 300}}},
		{pred.Pred{}, nil, &api.AggSpec{}, []api.Row{{"amount": 1090}}},
		{pred.Pred{Amount: pred.IGe(100)}, nil, &api.AggSpec{Group: []string{"region"}}, []api.Row{
			{"region": "east", "amount": 320}, {"region": "north", "amount": 100},
			{"region": "west", "amount": 450}}},
	}
	for i, c := range cs {
		got, err := e.Query(c.p, c.pj, c.ag)
		if err != nil || !sameRows(got, c.w) {
			t.Fatalf("Q%d got %v err=%v want %v", i+1, got, err, c.w)
		}
	}
}
func TestSelfCheck(t *testing.T) {
	if err := newEngine(t).SelfCheck(); err != nil {
		t.Fatalf("self-check: %v", err)
	}
}
func TestSentinelErrors(t *testing.T) {
	e := newEngine(t)
	if _, err := e.Query(pred.Pred{}, []string{"bogus"}, nil); err != api.ErrBadColumn {
		t.Fatal("bad projection column must be ErrBadColumn")
	}
	if _, err := e.Query(pred.Pred{}, nil, &api.AggSpec{Group: []string{"amount"}}); err != api.ErrBadColumn {
		t.Fatal("amount as group key must be ErrBadColumn")
	}
	if _, err := e.Query(pred.Pred{Region: pred.Atom{Op: pred.Lt, Int: 1}}, nil, nil); err != api.ErrBadPredicate {
		t.Fatal("region range atom must be ErrBadPredicate")
	}
	if _, err := api.New([]api.Row{{"amount": -1}}); err != api.ErrBadRows {
		t.Fatal("negative amount must be ErrBadRows")
	}
	if _, err := api.New([]api.Row{{"nope": 1}}); err != api.ErrBadRows {
		t.Fatal("unknown base column must be ErrBadRows")
	}
	if api.ErrBadColumn == api.ErrBadPredicate || api.ErrBadColumn == api.ErrBadRows ||
		api.ErrBadPredicate == api.ErrBadRows {
		t.Fatal("the three sentinels must be distinct")
	}
}
func TestRejectedLeavesState(t *testing.T) {
	e := newEngine(t)
	q := func() []api.Row {
		r, _ := e.Query(pred.Pred{Region: pred.RegionEq("east")}, []string{"region", "amount"}, nil)
		return r
	}
	before := q()
	if err := e.RegisterFilter("b", pred.Pred{Region: pred.Atom{Op: pred.Gt}}, []string{"region"}); err != api.ErrBadPredicate {
		t.Fatal("expected ErrBadPredicate")
	}
	if err := e.RegisterFilter("b2", pred.Pred{}, []string{"zzz"}); err != api.ErrBadColumn {
		t.Fatal("expected ErrBadColumn")
	}
	if !sameRows(before, q()) {
		t.Fatal("registered view set changed after a rejection")
	}
	if err := e.SelfCheck(); err != nil {
		t.Fatalf("engine not usable after rejection: %v", err)
	}
}
func TestConcurrent(t *testing.T) {
	e := newEngine(t)
	p := pred.Pred{Region: pred.RegionEq("east"), Amount: pred.IGe(100)}
	const n = 64
	res := make([][]api.Row, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := e.Query(p, []string{"region", "amount"}, nil)
			if err != nil {
				t.Error(err)
			}
			res[i] = r
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if !sameRows(res[0], res[i]) {
			t.Fatalf("goroutine %d returned a different result", i)
		}
	}
}
