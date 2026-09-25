package api_test

import (
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"

	"ontology/api"
)

// naive is the invariant-1 oracle: -1 means unseen; late events dropped; stable TS sort keeps arrival ties.
func naive(seq []api.Event) []api.Event {
	acc, w := []api.Event{}, [2]int64{-1, -1}
	for _, e := range seq {
		k := int(e.Stream - 'A')
		if e.TS < w[k] {
			continue
		}
		w[k] = max(w[k], e.TS)
		acc = append(acc, e)
	}
	sort.SliceStable(acc, func(i, j int) bool { return acc[i].TS < acc[j].TS })
	return acc
}
func isSorted(v []api.Event) bool {
	for i := 1; i < len(v); i++ {
		if v[i].TS < v[i-1].TS {
			return false
		}
	}
	return true
}
func TestNaiveReorder(t *testing.T) {
	for seed := int64(0); seed < 30; seed++ {
		rng, a, all := rand.New(rand.NewSource(seed)), api.New(), []api.Event{}
		seq := make([]api.Event, 1+rng.Intn(120))
		for i := range seq {
			seq[i] = api.Event{Stream: byte('A' + rng.Intn(2)), TS: int64(rng.Intn(12))}
			out, err := a.Feed(seq[i])
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, out...)
		}
		rest, err := a.Close()
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, rest...)
		if !isSorted(all) || !reflect.DeepEqual(all, naive(seq)) {
			t.Fatalf("seed=%d got %v want %v", seed, all, naive(seq))
		}
	}
}
func TestNonDecreasing(t *testing.T) {
	a := api.New()
	for _, e := range []api.Event{{Stream: 'A', TS: 5}, {Stream: 'B', TS: 9}, {Stream: 'A', TS: 6}} {
		a.Feed(e)
	}
	v, _ := a.Close()
	if !isSorted(v) {
		t.Fatal("decrease")
	}
}
func TestNoEarlyEmission(t *testing.T) {
	a := api.New()
	if out, _ := a.Feed(api.Event{Stream: 'A', TS: 1}); len(out) != 0 { // B unseen -> W=-inf
		t.Fatalf("lone stream emitted %v", out)
	}
	a.Feed(api.Event{Stream: 'B', TS: 3})                               // W=1: A1 emits; B3 stays buffered
	if out, _ := a.Feed(api.Event{Stream: 'B', TS: 8}); len(out) != 0 { // wA=1 -> W=1
		t.Fatalf("emitted above W: %v", out)
	}
}
func TestRejectedLeavesNoTrace(t *testing.T) {
	if errors.Is(api.ErrBadStream, api.ErrNegativeTS) || errors.Is(api.ErrBadStream, api.ErrClosed) ||
		errors.Is(api.ErrNegativeTS, api.ErrClosed) {
		t.Fatal("sentinels not distinct")
	}
	f := func(e api.Event) func(*api.Aligner) error {
		return func(a *api.Aligner) error { _, err := a.Feed(e); return err }
	}
	cases := []struct {
		pre  func(*api.Aligner)
		op   func(*api.Aligner) error
		want error
	}{
		{nil, f(api.Event{Stream: 'C', TS: 1}), api.ErrBadStream},
		{nil, f(api.Event{Stream: 'A', TS: -1}), api.ErrNegativeTS},
		{func(a *api.Aligner) { a.Close() }, f(api.Event{Stream: 'A', TS: 1}), api.ErrClosed},
		{func(a *api.Aligner) { a.Close() }, func(a *api.Aligner) error { _, e := a.Close(); return e }, api.ErrClosed},
	}
	for _, tc := range cases {
		a := api.New()
		a.Feed(api.Event{Stream: 'A', TS: 5})
		if tc.pre != nil {
			tc.pre(a)
		}
		snap := a.View()
		if err := tc.op(a); !errors.Is(err, tc.want) || !reflect.DeepEqual(a.View(), snap) || a.Dropped() != 0 {
			t.Fatalf("case %v: error/state mismatch", tc.want)
		}
	}
	a := api.New()
	if _, e := a.Feed(api.Event{Stream: 'X', TS: 1}); !errors.Is(e, api.ErrBadStream) {
		t.Fatal(e)
	}
	if _, e := a.Feed(api.Event{Stream: 'A', TS: 1}); e != nil {
		t.Fatal("unusable after reject")
	}
}
func TestConcurrentReaders(t *testing.T) {
	a, rng := api.New(), rand.New(rand.NewSource(7))
	for i := 0; i < 200; i++ {
		a.Feed(api.Event{Stream: byte('A' + i%2), TS: int64(rng.Intn(50))})
	}
	a.Close()
	const n = 24
	var wg sync.WaitGroup
	start, views := make(chan struct{}), make([][]api.Event, n)
	for g := range views {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			views[g] = a.View()
			if a.Dropped() < 0 || a.SelfCheck() != nil {
				t.Errorf("concurrent read failed")
			}
		}(g)
	}
	close(start)
	wg.Wait()
	for g := 1; g < n; g++ {
		if !reflect.DeepEqual(views[g], views[0]) {
			t.Fatalf("reader %d differs", g)
		}
	}
}
func TestSelfCheckBuiltin(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
