package api

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/evol"
	"ontology/sch"
)

func naive(cat *sch.Catalog, rec *evol.Record, R int) map[string]int {
	m := map[string]int{}
	for _, f := range cat.Fields(R) {
		m[f] = cat.Def(f, rec.W)
		if v, ok := rec.Values()[f]; ok {
			m[f] = v
		}
	}
	return m
}
func TestNaiveReference(t *testing.T) {
	a, cat := New(), sch.NewStandard()
	cases := []struct {
		W  int
		vs map[string]int
		Rs []int
	}{
		{1, map[string]int{"a": 9, "b": 7}, []int{1, 2, 3}},
		{2, map[string]int{"a": 5}, []int{1, 2, 3}},
		{3, map[string]int{"a": 1, "c": 2, "d": 3}, []int{1, 2, 3}},
	}
	for _, c := range cases {
		rec, err := a.Write(c.W, c.vs)
		if err != nil {
			t.Fatal(err)
		}
		for _, R := range c.Rs {
			got, err := a.Read(rec, R)
			if err != nil || !equal(got, naive(cat, rec, R)) {
				t.Fatalf("W=%d R=%d got %v err %v", c.W, R, got, err)
			}
		}
	}
}
func TestBackwardCompatible(t *testing.T) {
	a, cat := New(), sch.NewStandard()
	full := []map[string]int{{"a": 1, "b": 2}, {"a": 1, "b": 2, "c": 10}, {"a": 1, "c": 30, "d": 20}}
	for W := 1; W <= 3; W++ {
		rec, err := a.Write(W, full[W-1])
		if err != nil {
			t.Fatal(err)
		}
		for R := W; R <= 3; R++ {
			got, _ := a.Read(rec, R)
			for _, f := range cat.Fields(W) {
				if cat.Has(R, f) && got[f] != full[W-1][f] {
					t.Fatalf("W=%d R=%d %s changed", W, R, f)
				}
			}
		}
	}
}
func TestFrozenDefaults(t *testing.T) {
	a := New()
	rec, err := a.Write(1, map[string]int{"a": 9})
	if err != nil {
		t.Fatal(err)
	}
	want := []map[string]int{nil, {"a": 9, "b": 2}, {"a": 9, "b": 2, "c": 10}, {"a": 9, "c": 10, "d": 20}}
	for R := 1; R <= 3; R++ {
		got, err := a.Read(rec, R)
		if err != nil || !equal(got, want[R]) || (R >= 2 && got["c"] != 10) {
			t.Fatalf("R=%d got %v err %v", R, got, err)
		}
	}
}
func TestRejectionLeavesNoTrace(t *testing.T) {
	a := New()
	good, _ := a.Write(1, map[string]int{"a": 9, "b": 7})
	werr := func(W int, vs map[string]int) error { _, e := a.Write(W, vs); return e }
	rerr := func(R int) error { _, e := a.Read(good, R); return e }
	bad := []struct {
		err  error
		want error
	}{
		{werr(0, nil), evol.ErrBadWriteVersion},
		{werr(4, nil), evol.ErrBadWriteVersion},
		{werr(1, map[string]int{"z": 1}), evol.ErrFieldOutOfScope},
		{werr(1, map[string]int{"": 1}), evol.ErrFieldOutOfScope},
		{rerr(0), evol.ErrBadReadVersion},
		{rerr(4), evol.ErrBadReadVersion},
	}
	for i, c := range bad {
		if !errors.Is(c.err, c.want) {
			t.Fatalf("case %d got %v want %v", i, c.err, c.want)
		}
	}
	got, err := a.Read(good, 3)
	if err != nil || !equal(got, map[string]int{"a": 9, "c": 10, "d": 20}) {
		t.Fatalf("state changed after rejection: %v %v", got, err)
	}
	if evol.ErrBadWriteVersion == evol.ErrFieldOutOfScope || evol.ErrFieldOutOfScope == evol.ErrBadReadVersion {
		t.Fatal("sentinel errors not distinct")
	}
}
func TestConcurrentReads(t *testing.T) {
	a := New()
	rec, _ := a.Write(2, map[string]int{"a": 5})
	want := map[int]map[string]int{}
	for R := 1; R <= 3; R++ {
		want[R], _ = a.Read(rec, R)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var bad atomic.Int32
	run := func(n int, fn func(int)) {
		for g := 0; g < n; g++ {
			wg.Add(1)
			go func(g int) { defer wg.Done(); <-start; fn(g) }(g)
		}
	}
	run(16, func(g int) {
		for i := 0; i < 200; i++ {
			if m, e := a.Read(rec, g%3+1); e != nil || !equal(m, want[g%3+1]) {
				bad.Add(1)
			}
		}
	})
	run(4, func(g int) {
		for i := 0; i < 100; i++ {
			if _, e := a.Write(1, map[string]int{"a": g*100 + i}); e != nil {
				bad.Add(1)
			}
		}
	})
	run(4, func(int) {
		for i := 0; i < 20; i++ {
			if a.SelfCheck() != nil {
				bad.Add(1)
			}
		}
	})
	close(start)
	wg.Wait()
	if bad.Load() != 0 {
		t.Fatalf("%d concurrent failures", bad.Load())
	}
}
