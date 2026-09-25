package api_test

import (
	"errors"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/api"
	"ontology/idx"
)

func naiveJoin(s, r []int) (out []api.Pair) {
	for _, rk := range r {
		for _, sk := range s {
			if rk == sk {
				out = append(out, api.Pair{R: rk, S: sk})
			}
		}
	}
	return out
}

func probe(t *testing.T, s, r []int) []api.Pair {
	sess := api.New()
	if err := sess.BuildIndex(s); err != nil {
		t.Fatalf("BuildIndex(%v): %v", s, err)
	}
	got, err := sess.Probe(r)
	if err != nil {
		t.Fatalf("Probe(%v): %v", r, err)
	}
	return got
}

// TestProbeMatchesNaive pins invariant 1: probe output == naive nested loop.
func TestProbeMatchesNaive(t *testing.T) {
	cases := []struct{ s, r []int }{
		{[]int{2, 3, 3, 3, 5, 7}, []int{3, 5, 1, 3}},
		{[]int{0}, []int{0, 0, 1}},
	}
	rng := rand.New(rand.NewSource(1))
	for _, n := range []int{1, 10, 100, 1000} { // random tiers
		s, r := make([]int, n), make([]int, n)
		for i := range s {
			s[i], r[i] = rng.Intn(n/2+1), rng.Intn(n/2+2)
		}
		slices.Sort(s)
		cases = append(cases, struct{ s, r []int }{s, r})
	}
	for _, c := range cases {
		if got, want := probe(t, c.s, c.r), naiveJoin(c.s, c.r); !slices.Equal(got, want) {
			t.Errorf("s=%v r=%v: got %v, want %v", c.s, c.r, got, want)
		}
	}
}

// TestBoundsExact pins invariant 2: [lo, hi) holds exactly the keys == k.
func TestBoundsExact(t *testing.T) {
	x, _ := idx.Build([]int{2, 3, 3, 3, 5, 7})
	cases := []struct{ k, lo, hi int }{
		{3, 1, 4}, {5, 4, 5}, {1, 0, 0}, {2, 0, 1}, {7, 5, 6}, {8, 6, 6}, {0, 0, 0},
	}
	for _, c := range cases {
		if lo, hi := x.Bounds(c.k, nil); lo != c.lo || hi != c.hi {
			t.Errorf("Bounds(%d) = [%d,%d), want [%d,%d)", c.k, lo, hi, c.lo, c.hi)
		}
	}
}

// TestErrorsDistinguishable: three distinct decidable sentinel errors.
func TestErrorsDistinguishable(t *testing.T) {
	sess := api.New()
	probeErr := func(r []int) error { _, e := sess.Probe(r); return e }
	cases := []struct {
		name      string
		got, want error
	}{
		{"build nil", sess.BuildIndex(nil), api.ErrNilInput},
		{"build negative", sess.BuildIndex([]int{3, -1}), api.ErrNegativeKey},
		{"build unsorted", sess.BuildIndex([]int{2, 1}), api.ErrNotSorted},
		{"probe nil", probeErr(nil), api.ErrNilInput},
		{"probe negative", probeErr([]int{-2}), api.ErrNegativeKey},
	}
	for _, c := range cases {
		if !errors.Is(c.got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
	sents := []error{api.ErrNilInput, api.ErrNegativeKey, api.ErrNotSorted}
	if errors.Is(sents[0], sents[1]) || errors.Is(sents[1], sents[2]) || errors.Is(sents[0], sents[2]) {
		t.Error("sentinel errors not distinguishable")
	}
}

// TestRejectLeavesStateUnchanged pins invariant 4: rejections leave no trace.
func TestRejectLeavesStateUnchanged(t *testing.T) {
	sess := api.New()
	if err := sess.BuildIndex([]int{2, 3, 3, 3, 5, 7}); err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	must := func() []api.Pair {
		got, err := sess.Probe([]int{3, 5, 1, 3})
		if err != nil {
			t.Fatalf("Probe: %v", err)
		}
		return got
	}
	before := must()
	for _, bad := range [][]int{nil, {-1}, {2, 1}} {
		_ = sess.BuildIndex(bad)
		_, _ = sess.Probe(bad)
	}
	if after := must(); !slices.Equal(before, after) {
		t.Errorf("state changed by rejected ops: %v -> %v", before, after)
	}
}

// TestConcurrentProbeIdentical: N goroutines of read-only probes on the same R. No sleeps, -race clean.
func TestConcurrentProbeIdentical(t *testing.T) {
	r := []int{3, 5, 1, 3}
	sess := api.New()
	if err := sess.BuildIndex([]int{2, 3, 3, 3, 5, 7}); err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	want := probe(t, []int{2, 3, 3, 3, 5, 7}, r)
	var wg sync.WaitGroup
	fail := make(chan []api.Pair, 16)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, err := sess.Probe(r); err != nil || !slices.Equal(got, want) {
				fail <- got
			}
		}()
	}
	wg.Wait()
	close(fail)
	for got := range fail {
		t.Errorf("concurrent probe got %v, want %v", got, want)
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
