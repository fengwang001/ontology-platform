package api

import (
	"errors"
	"maps"
	"math/rand"
	"strconv"
	"sync"
	"testing"

	"ontology/alloc"
	"ontology/mf"
)

func eqMap(a, b map[string]mf.Frac) bool {
	return maps.EqualFunc(a, b, func(x, y mf.Frac) bool { return x.Cmp(y) == 0 })
}

func tk(id string, d int64) alloc.Task { return alloc.Task{ID: id, Demand: d} }

func build(t *testing.T, c int64, ts []alloc.Task) *Allocator {
	t.Helper()
	a, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range ts {
		if err := a.Add(s.ID, s.Demand); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

func mktasks(rng *rand.Rand, n int) ([]alloc.Task, int64) {
	ts := make([]alloc.Task, n)
	var sum int64
	for i := range ts {
		ts[i] = tk("t"+strconv.Itoa(i), int64(rng.Intn(20))) // zero demand allowed
		sum += ts[i].Demand
	}
	return ts, sum
}

func TestAllocateMatchesNaive(t *testing.T) {
	tab := []struct {
		c  int64
		ts []alloc.Task
	}{
		{30, []alloc.Task{tk("A", 6), tk("B", 12), tk("C", 18), tk("D", 30)}},
		{100, []alloc.Task{tk("A", 6), tk("B", 12), tk("C", 18), tk("D", 30), tk("Z", 0)}},
		{10, []alloc.Task{tk("z", 0), tk("x", 0)}},
		{20, []alloc.Task{tk("p", 10), tk("q", 10), tk("r", 10)}},
	}
	for i, e := range tab {
		if !eqMap(build(t, e.c, e.ts).Allocate(), naive(e.c, e.ts)) {
			t.Fatalf("case %d mismatch vs naive", i)
		}
	}
	rng := rand.New(rand.NewSource(99))
	for it := 0; it < 200; it++ { // arbitrary Add order must not change the result
		n := 1 + rng.Intn(12)
		ts, _ := mktasks(rng, n)
		c := int64(1 + rng.Intn(120))
		a, _ := New(c)
		for _, i := range rng.Perm(n) {
			if err := a.Add(ts[i].ID, ts[i].Demand); err != nil {
				t.Fatal(err)
			}
		}
		if !eqMap(a.Allocate(), naive(c, ts)) {
			t.Fatalf("random %d mismatch vs naive", it)
		}
	}
}

func TestMaxMinLevel(t *testing.T) {
	rng := rand.New(rand.NewSource(123))
	for it := 0; it < 200; it++ {
		n := 1 + rng.Intn(12)
		ts, sum := mktasks(rng, n)
		c := int64(1 + rng.Intn(120))
		got := build(t, c, ts).Allocate()
		if !level(ts, got) || !alloc.SumEquals(got, min(c, sum)) {
			t.Fatalf("%d: max-min level or conservation failed", it)
		}
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrInvalidCapacity) {
		t.Fatalf("New(0) err=%v", err)
	}
	a := build(t, 30, []alloc.Task{tk("A", 6), tk("B", 12)})
	before := a.Allocate()
	e0, e1, e2 := a.Add("", 1), a.Add("A", 1), a.Add("Q", -1) // three distinct, judgeable errors
	if !errors.Is(e0, ErrEmptyID) || errors.Is(e0, ErrDuplicateID) || !errors.Is(e1, ErrDuplicateID) ||
		errors.Is(e1, ErrEmptyID) || !errors.Is(e2, ErrNegativeDemand) || errors.Is(e2, ErrDuplicateID) {
		t.Fatalf("errors not distinct: %v %v %v", e0, e1, e2)
	}
	if !eqMap(a.Allocate(), before) {
		t.Fatal("rejected operations changed state")
	}
	if err := a.Add("C", 18); err != nil { // still usable after rejections
		t.Fatalf("allocator unusable after rejection: %v", err)
	}
}

func TestSelfCheck(t *testing.T) {
	a, err := New(30)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestConcurrentAdd(t *testing.T) {
	for _, n := range []int{50, 200, 1000} {
		c := int64(1000)
		par, _ := New(c)
		seq, _ := New(c)
		rng := rand.New(rand.NewSource(int64(n)))
		ts, sum := mktasks(rng, n)
		var wg sync.WaitGroup
		for _, i := range rng.Perm(n) { // WaitGroup, no sleep; arbitrary order
			_ = seq.Add(ts[i].ID, ts[i].Demand)
			wg.Add(1)
			go func(s alloc.Task) {
				defer wg.Done()
				if err := par.Add(s.ID, s.Demand); err != nil {
					t.Error(err)
				}
			}(ts[i])
		}
		wg.Wait()
		if !eqMap(par.Allocate(), seq.Allocate()) || !alloc.SumEquals(par.Allocate(), min(c, sum)) {
			t.Fatalf("n=%d: concurrent mismatch or not conserved", n)
		}
	}
}
