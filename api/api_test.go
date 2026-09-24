package api_test

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/thr"
)

type scale struct {
	w          int64
	nk, pk, sd int
}

var scales = []scale{{3, 5, 6, 1}, {3, 8, 4, 2}, {5, 12, 7, 3}, {1, 20, 10, 4}}

func must(t *testing.T, e error) {
	if e != nil {
		t.Fatal(e)
	}
}
func newR(t *testing.T, w, m int64) *api.Refresher { r, e := api.New(w, m); must(t, e); return r }
func eqRef(t *testing.T, got, want []api.Refresh) {
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// checkFeed drives a random non-decreasing stream (shuffled per round ->
// equal-t ties, later arrival wins), ticks, then Stops; pins invariants 1-2.
func checkFeed(t *testing.T, s scale) {
	r := newR(t, s.w, int64(s.nk)*8)
	rn := rand.New(rand.NewSource(int64(s.sd)))
	ref, cur := map[string]string{}, int64(0)
	for rd := 0; rd < s.pk; rd++ {
		for _, ki := range rn.Perm(s.nk) {
			k, v := string(rune('a'+ki)), string(rune('A'+rd))
			must(t, r.Record(k, cur, v))
			ref[k] = v
		}
		r.Tick(cur + s.w + 1)
		cur += s.w + 1 + rn.Int63n(3)
	}
	r.Stop(cur + 100)
	if !reflect.DeepEqual(r.View(), ref) {
		t.Fatalf("invariant1: %v", r.View())
	}
	if r.Fired() > s.nk*s.pk {
		t.Fatalf("invariant2: fired %d > accepted %d", r.Fired(), s.nk*s.pk)
	}
}
func TestInvariants_FinalAndThrottle(t *testing.T) {
	for _, s := range scales {
		checkFeed(t, s)
	}
}
func TestInvariant_Monotonic(t *testing.T) {
	r := newR(t, 3, 8)
	must(t, r.Record("k", 5, "a"))
	if !errors.Is(r.Record("k", 4, "z"), thr.ErrClockBack) || r.Tick(3) != nil {
		t.Fatal("clock regression accepted")
	}
	if v, f := r.View(), r.Fired(); len(v) != 0 || f != 0 {
		t.Fatal("rejected regression left a trace")
	}
	must(t, r.Record("k", 5, "b")) // t == last: still accepted
}
func TestRejectionLeavesNoTrace(t *testing.T) {
	errs := []error{thr.ErrBadParam, thr.ErrEmptyKey, thr.ErrClockBack, thr.ErrTooMany}
	if errs[0] == errs[1] || errs[0] == errs[2] || errs[0] == errs[3] || errs[1] == errs[2] || errs[1] == errs[3] || errs[2] == errs[3] {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
	for _, p := range [][2]int64{{0, 1}, {1, 0}} {
		if _, e := api.New(p[0], p[1]); !errors.Is(e, thr.ErrBadParam) {
			t.Fatalf("bad param %v: %v", p, e)
		}
	}
	r := newR(t, 3, 1)
	must(t, r.Record("a", 0, "1"))
	for i, f := range []func() error{
		func() error { return r.Record("", 1, "x") },
		func() error { return r.Record("a", -1, "x") },
		func() error { return r.Record("b", 0, "2") },
	} {
		if e := f(); !errors.Is(e, errs[i+1]) {
			t.Fatalf("case %d: got %v", i, e)
		}
	}
	if v, f := r.View(), r.Fired(); len(v) != 0 || f != 0 {
		t.Fatal("rejection changed state")
	}
	eqRef(t, r.Stop(2), []api.Refresh{{Key: "a", N: 1, Val: "1"}})
}
func TestEightSteps(t *testing.T) {
	r := newR(t, 3, 8)
	must(t, r.Record("k", 0, "a"))
	must(t, r.Record("k", 2, "b"))
	must(t, r.Record("m", 2, "x"))
	eqRef(t, r.Tick(4), nil)
	must(t, r.Record("k", 5, "c")) // step5: t == old Due(5), still merges
	eqRef(t, r.Tick(5), []api.Refresh{{Key: "m", N: 1, Val: "x"}})
	must(t, r.Record("m", 5, "y"))
	eqRef(t, r.Stop(8), []api.Refresh{{Key: "k", N: 3, Val: "c"}, {Key: "m", N: 1, Val: "y"}})
	if !reflect.DeepEqual(r.View(), map[string]string{"k": "c", "m": "y"}) {
		t.Fatalf("final view %v", r.View())
	}
	q := newR(t, 3, 4) // gap exactly W, no Tick between: still one batch
	must(t, q.Record("q", 0, "a"))
	must(t, q.Record("q", 3, "b"))
	eqRef(t, q.Tick(2), nil)
	eqRef(t, q.Tick(6), []api.Refresh{{Key: "q", N: 2, Val: "b"}})
}
func TestConcurrentReadOnly(t *testing.T) {
	r := newR(t, 3, 64)
	var wg sync.WaitGroup
	start, done := make(chan struct{}), make(chan struct{})
	vs, fs := make([]map[string]string, 8), make([]int, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for j := 0; j < 64; j++ {
				r.View()
				r.Fired()
				_ = r.SelfCheck()
			}
			<-done // writer finished: read settled state
			vs[g], fs[g] = r.View(), r.Fired()
		}(g)
	}
	close(start) // readers interleave with the single writer
	for i := 0; i < 64; i++ {
		must(t, r.Record(string(rune('a'+i%26)), int64(i), "w"))
	}
	r.Tick(500)
	r.Stop(1000) // Tick/Stop also interleave with live readers
	close(done)
	wg.Wait()
	for g := 1; g < 8; g++ {
		if !reflect.DeepEqual(vs[g], vs[0]) || fs[g] != fs[0] {
			t.Fatalf("reader %d diverged", g)
		}
	}
}
func TestSelfCheck(t *testing.T) { must(t, newR(t, 3, 8).SelfCheck()) }
