package api

import (
	"errors"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"ontology/pred"
	"ontology/rewrite"
)

var ra2, r3 = []string{pred.Region, pred.Amount}, []string{pred.Region, pred.Amount, pred.Year}

func must(t *testing.T, e error) {
	if e != nil {
		t.Helper()
		t.Fatal(e)
	}
}
func regTestViews(t *testing.T, d *DB) {
	must(t, d.RegisterFilter("V1", pred.Pred{Region: sp("east")}, ra2))
	must(t, d.RegisterFilter("V2", pred.Pred{Amount: pred.I(pred.Ge, 100)}, r3))
	must(t, d.RegisterAgg("V3", pred.Pred{}, []string{pred.Region}))
}
func TestViewContainment(t *testing.T) {
	i := pred.I
	for _, c := range []struct {
		p, v pred.Pred
		ok   bool
	}{
		{pred.Pred{Amount: i(pred.Gt, 100)}, pred.Pred{Amount: i(pred.Ge, 100)}, true},
		{pred.Pred{Amount: i(pred.Ge, 100)}, pred.Pred{Amount: i(pred.Gt, 100)}, false},
		{pred.Pred{Amount: i(pred.Lt, 10)}, pred.Pred{Amount: i(pred.Le, 10)}, true},
		{pred.Pred{Amount: i(pred.Le, 10)}, pred.Pred{Amount: i(pred.Lt, 10)}, false},
		{pred.Pred{Region: sp("east")}, pred.Pred{}, true},
		{pred.Pred{}, pred.Pred{Region: sp("east")}, false},
		{pred.Pred{Region: sp("east")}, pred.Pred{Region: sp("west")}, false},
	} {
		if pred.Contains(c.p, c.v) != c.ok {
			t.Fatalf("containment %+v", c)
		}
	}
	db, _ := New(baseRows())
	regTestViews(t, db)
	r4, _ := db.Query(pred.Pred{Region: sp("east")}, r3, nil)
	r7, _ := db.Query(pred.Pred{Amount: i(pred.Ge, 100)}, nil, &AggSpec{Group: []string{pred.Region}})
	if len(r4) != 4 || r4[0][pred.Year] != 2020 || len(r7) != 3 || r7[0][pred.Amount] != 320 {
		t.Fatalf("Q4=%v Q7=%v", r4, r7)
	}
}
func TestResidualCompleteness(t *testing.T) {
	for _, o1 := range []pred.Op{pred.Eq, pred.Lt, pred.Le, pred.Gt, pred.Ge} {
		for _, o2 := range []pred.Op{pred.Eq, pred.Lt, pred.Le, pred.Gt, pred.Ge} {
			p, vp := pred.Pred{Amount: pred.I(o1, 100)}, pred.Pred{Amount: pred.I(o2, 100)}
			if pred.Contains(p, vp) {
				for _, x := range []int{99, 100, 101} {
					r := pred.Residual(p, vp)
					if vp.Match("z", x, 1) && r.Match("z", x, 1) != p.Match("z", x, 1) {
						t.Fatalf("o1=%v o2=%v x=%d", o1, o2, x)
					}
				}
			}
		}
	}
}
func TestRewriteMatchesFullScan(t *testing.T) {
	base := baseRows()
	db, _ := New(base)
	regTestViews(t, db)
	for _, rg := range []*string{nil, sp("east"), sp("west"), sp("north")} {
		for _, am := range []*pred.Atom{nil, pred.I(pred.Eq, 100), pred.I(pred.Lt, 120), pred.I(pred.Gt, 100), pred.I(pred.Ge, 200)} {
			for _, yr := range []*pred.Atom{nil, pred.I(pred.Ge, 2021)} {
				p := pred.Pred{Region: rg, Amount: am, Year: yr}
				for _, c := range [][]string{ra2, r3} {
					if g, e := db.Query(p, c, nil); e != nil || !reflect.DeepEqual(g, sel(base, p, c)) {
						t.Fatalf("ordinary %v %v err=%v", g, c, e)
					}
				}
				for _, x := range [][]string{nil, {pred.Region}, {pred.Year}, {pred.Region, pred.Year}} {
					if g, e := db.Query(p, nil, &AggSpec{Group: x}); e != nil || !reflect.DeepEqual(g, aggregate(base, p, x)) {
						t.Fatalf("aggregate g=%v %v err=%v", x, g, e)
					}
				}
			}
		}
	}
	must(t, SelfCheck())
}
func TestRejectedOpsLeaveState(t *testing.T) {
	db, _ := New(baseRows())
	regTestViews(t, db)
	if ErrInvalidRow == rewrite.ErrInvalidColumn || ErrInvalidRow == pred.ErrInvalidAtom || rewrite.ErrInvalidColumn == pred.ErrInvalidAtom {
		t.Fatal("sentinels must be pairwise distinct")
	}
	_, e1 := New([]Row{{pred.Region: "x", pred.Amount: -1, pred.Year: 1}})
	_, e2 := New([]Row{{pred.Region: "x", pred.Amount: 1, pred.Year: 1, "z": 2}})
	e3 := db.RegisterFilter("b", pred.Pred{}, []string{"nope"})
	e4 := db.RegisterAgg("b", pred.Pred{}, []string{pred.Amount})
	_, e5 := db.Query(pred.Pred{}, []string{"nope"}, nil)
	_, e6 := pred.Build(pred.RawAtom{Col: pred.Region, Op: pred.Lt})
	got := []error{e1, e2, e3, e4, e5, e6}
	want := []error{ErrInvalidRow, ErrInvalidRow, rewrite.ErrInvalidColumn,
		rewrite.ErrInvalidColumn, rewrite.ErrInvalidColumn, pred.ErrInvalidAtom}
	for i := range got {
		if !errors.Is(got[i], want[i]) {
			t.Fatalf("case %d: got %v want %v", i, got[i], want[i])
		}
	}
	must(t, db.RegisterFilter("b", pred.Pred{Region: sp("north")}, ra2)) // rejected name is free
	if rs, _ := db.Query(pred.Pred{Region: sp("east")}, ra2, nil); len(rs) != 4 {
		t.Fatal("registered view set changed after rejection")
	}
}
func TestScaledCandidateCount(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		db, _ := New(nil)
		for i := 0; i < m; i++ {
			s := "r" + strconv.Itoa(i)
			must(t, db.RegisterFilter("v"+s, pred.Pred{Region: &s}, []string{pred.Region}))
		}
		s := "r" + strconv.Itoa(m/2)
		_, e := db.Query(pred.Pred{Region: &s}, []string{pred.Region}, nil)
		must(t, e)
	}
	if !rewrite.ScaleCheck() { // counter read only inside package; bool out
		t.Fatal("candidate comparisons grow linearly with m")
	}
}
func TestConcurrentQueries(t *testing.T) {
	db, _ := New(baseRows())
	regTestViews(t, db)
	const n = 32
	var wg sync.WaitGroup
	got := make([][]Row, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			got[i], _ = db.Query(pred.Pred{Amount: pred.I(pred.Gt, 100)}, ra2, nil)
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if !reflect.DeepEqual(got[i], got[0]) {
			t.Fatalf("goroutine %d differs", i)
		}
	}
}
