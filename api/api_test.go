package api

import (
	"errors"
	"math/rand"
	"sort"
	"sync"
	"testing"

	"ontology/mrg"
	"ontology/run"
)

func mustJoin(t *testing.T, e *Engine) []Pair {
	t.Helper()
	p, err := e.Join()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Invariant 1: Join output multiset equals the naive nested loop.
func TestInvariant_NestedLoopEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	cases := []struct{ m, fanIn, nR, nS, keySpace int }{
		{3, 2, 6, 4, 8}, {1, 2, 50, 40, 5}, {2, 3, 200, 150, 20},
		{7, 4, 1000, 800, 100}, {5, 2, 0, 10, 3}, {4, 8, 333, 0, 2},
	}
	for _, c := range cases {
		mk := func(n int) []Key {
			ks := make([]Key, n)
			for i := range ks {
				ks[i] = rng.Intn(c.keySpace)
			}
			return ks
		}
		r, s := mk(c.nR), mk(c.nS)
		e, err := New(c.m, c.fanIn)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.BuildR(r); err != nil {
			t.Fatal(err)
		}
		if err := e.BuildS(s); err != nil {
			t.Fatal(err)
		}
		if got := mustJoin(t, e); !multisetEqual(got, naive(r, s)) {
			t.Fatalf("case %+v: join multiset != naive", c)
		}
	}
}

// Invariant 2: each side's merged sequence is its input sorted.
func TestInvariant_MergedSorted(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, c := range []struct{ m, fanIn, n int }{{3, 2, 6}, {1, 2, 9}, {4, 3, 100}, {2, 2, 257}} {
		keys := make([]int, c.n)
		for i := range keys {
			keys[i] = rng.Intn(50)
		}
		runs, err := run.Build(c.m, keys)
		if err != nil {
			t.Fatal(err)
		}
		merged, err := mrg.Sorted(runs, c.fanIn)
		if err != nil {
			t.Fatal(err)
		}
		want := append([]int(nil), keys...)
		sort.Ints(want)
		if !sort.IntsAreSorted(merged) || !multisetEqualInts(merged, want) {
			t.Fatalf("case %+v: merged sequence is not the sorted input", c)
		}
	}
}

func multisetEqualInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := append([]int(nil), a...), append([]int(nil), b...)
	sort.Ints(x)
	sort.Ints(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// Invariant 3: all runs but the last hold exactly M tuples, the last
// holds <= M, and every run is internally ascending.
func TestInvariant_RunStructure(t *testing.T) {
	for _, c := range []struct{ m, n int }{{3, 6}, {3, 7}, {1, 5}, {4, 16}, {5, 3}, {2, 0}} {
		keys := make([]int, c.n)
		for i := range keys {
			keys[i] = (i * 37) % 11
		}
		runs, err := run.Build(c.m, keys)
		if err != nil {
			t.Fatal(err)
		}
		total := 0
		for i, r := range runs {
			if r.ID != i || !sort.IntsAreSorted(r.Keys) {
				t.Fatalf("case %+v: run %d bad id/order", c, i)
			}
			if i < len(runs)-1 && len(r.Keys) != c.m {
				t.Fatalf("case %+v: run %d has %d tuples, want %d", c, i, len(r.Keys), c.m)
			}
			if len(r.Keys) > c.m || len(r.Keys) == 0 {
				t.Fatalf("case %+v: run %d size %d out of bounds", c, i, len(r.Keys))
			}
			total += len(r.Keys)
		}
		if total != c.n {
			t.Fatalf("case %+v: runs hold %d tuples, want %d", c, total, c.n)
		}
	}
}

// Invariant 4: rejected operations fail atomically with distinct
// sentinel errors and leave state untouched.
func TestInvariant_RejectionAtomic(t *testing.T) {
	if _, err := New(0, 2); !errors.Is(err, ErrBadThreshold) {
		t.Fatalf("M<1: %v", err)
	}
	if _, err := New(1, 1); !errors.Is(err, ErrBadFanIn) {
		t.Fatalf("fanIn<2: %v", err)
	}
	if ErrBadThreshold == ErrBadFanIn || ErrBadFanIn == ErrNegativeKey ||
		ErrBadThreshold == ErrNegativeKey {
		t.Fatal("sentinel errors must be distinct")
	}
	e, _ := New(2, 2)
	if err := e.BuildR([]Key{3, 1}); err != nil {
		t.Fatal(err)
	}
	if err := e.BuildS([]Key{1, 3}); err != nil {
		t.Fatal(err)
	}
	before := mustJoin(t, e)
	for _, bad := range [][]Key{{1, -1}, {-5}} {
		if err := e.BuildR(bad); !errors.Is(err, ErrNegativeKey) {
			t.Fatalf("BuildR%v: %v", bad, err)
		}
		if err := e.BuildS(bad); !errors.Is(err, ErrNegativeKey) {
			t.Fatalf("BuildS%v: %v", bad, err)
		}
	}
	if got := mustJoin(t, e); !multisetEqual(got, before) {
		t.Fatal("rejected batch changed state")
	}
	if err := e.BuildR([]Key{2}); err != nil { // still usable afterwards
		t.Fatal(err)
	}
}

// Concurrency: N goroutines read-only Join must see identical results.
func TestConcurrentJoin(t *testing.T) {
	e, _ := New(3, 2)
	if err := e.BuildR([]Key{5, 1, 3, 7, 3, 2}); err != nil {
		t.Fatal(err)
	}
	if err := e.BuildS([]Key{3, 6, 3, 2}); err != nil {
		t.Fatal(err)
	}
	want := mustJoin(t, e)
	var wg sync.WaitGroup
	errs := make(chan bool, 16)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if got := mustJoin(t, e); !multisetEqual(got, want) {
					errs <- true
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	if len(errs) > 0 {
		t.Fatal("concurrent Join results differ")
	}
}

func TestSelfCheck(t *testing.T) {
	for _, c := range []struct{ m, fanIn int }{{1, 2}, {3, 2}, {4, 5}} {
		e, err := New(c.m, c.fanIn)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.SelfCheck(); err != nil {
			t.Fatalf("case %+v: %v", c, err)
		}
	}
}
