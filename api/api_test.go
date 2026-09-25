package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/api"
)

var (
	sIDs    = []string{"X", "L", "A", "B", "C", "D"}
	sArrive = []int64{0, 1, 2, 3, 4, 5}
	sLength = []int64{5, 10, 2, 3, 2, 1}
	sOrder  = []string{"X", "D", "A", "C", "B", "L"}
	sIv     = []api.Interval{{"X", 0, 5}, {"D", 5, 6}, {"A", 6, 8}, {"C", 8, 10}, {"B", 10, 13}, {"L", 13, 23}}
)

// naive is the tick-by-tick reference: at each idle moment scan every admitted
// unfinished job, take the (length, registration) minimum, and jump idle gaps.
func naive(ids []string, ar, ln []int64) []string {
	done, out := make([]bool, len(ids)), []string{}
	for t := int64(0); len(out) < len(ids); {
		cur, next := -1, int64(-1)
		for i := range ids {
			if !done[i] && ar[i] <= t && (cur < 0 || ln[i] < ln[cur] || ln[i] == ln[cur] && i < cur) {
				cur = i
			}
			if !done[i] && ar[i] > t && (next < 0 || ar[i] < next) {
				next = ar[i]
			}
		}
		if cur < 0 {
			t = next
			continue
		}
		t, done[cur], out = t+ln[cur], true, append(out, ids[cur])
	}
	return out
}

func build(t *testing.T, ids []string, ar, ln []int64) *api.API {
	a := api.New()
	for i := range ids {
		if err := a.Submit(ids[i], ar[i], ln[i]); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

// TestNaiveEquivalence pins invariant 1 on fixed and random table cases.
func TestNaiveEquivalence(t *testing.T) {
	check := func(ids []string, ar, ln []int64) {
		got := build(t, ids, ar, ln).Run()
		if want := naive(ids, ar, ln); !slices.Equal(got, want) {
			t.Errorf("got %v want %v", got, want)
		}
	}
	check(sIDs, sArrive, sLength)
	check([]string{"p", "q", "r"}, []int64{0, 0, 0}, []int64{3, 1, 2})
	check([]string{"a", "b", "c"}, []int64{10, 0, 5}, []int64{1, 1, 1})
	r := rand.New(rand.NewSource(42))
	for k := 0; k < 30; k++ {
		ids, ar, ln := []string{}, []int64{}, []int64{}
		for i, n := 0, 1+r.Intn(12); i < n; i++ {
			ids = append(ids, fmt.Sprintf("j%d", i))
			ar = append(ar, int64(r.Intn(20)))
			ln = append(ln, int64(1+r.Intn(8)))
		}
		check(ids, ar, ln)
	}
	if err := api.New().SelfCheck(); err != nil {
		t.Errorf("SelfCheck: %v", err)
	}
}

// TestNonPreemptive pins invariant 2: full-length back-to-back intervals, and
// an unstarted job waits -1.
func TestNonPreemptive(t *testing.T) {
	a := build(t, sIDs, sArrive, sLength)
	if a.Wait("X") != -1 || api.New().Wait("ghost") != -1 {
		t.Error("unstarted/unknown job must wait -1")
	}
	got := a.Run()
	if !slices.Equal(got, sOrder) || !slices.Equal(a.Intervals(), sIv) {
		t.Fatalf("order=%v intervals=%v", got, a.Intervals())
	}
}

// TestSelectionAndTie pins invariant 3: min length wins, ties by registration.
func TestSelectionAndTie(t *testing.T) {
	if got := build(t, []string{"a", "b", "c"}, []int64{0, 0, 0}, []int64{4, 4, 4}).Run(); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("tie order %v, want registration order", got)
	}
	if got := build(t, []string{"long", "short"}, []int64{0, 0}, []int64{9, 1}).Run(); got[0] != "short" {
		t.Errorf("selected %s, want shortest first", got[0])
	}
}

// TestRejectionLeavesNoTrace pins invariant 4: four distinct sentinel errors
// and no state change; the API stays usable afterwards.
func TestRejectionLeavesNoTrace(t *testing.T) {
	a := build(t, []string{"ok"}, []int64{0}, []int64{2})
	bad := []struct{ got, want error }{
		{a.Submit("", 0, 1), api.ErrEmptyID},
		{a.Submit("ok", 0, 1), api.ErrDuplicateID},
		{a.Submit("z", 0, 0), api.ErrNonPositiveLength},
		{a.Submit("z", -1, 1), api.ErrNegativeArrive},
	}
	seen := map[error]bool{}
	for i, b := range bad {
		if !errors.Is(b.got, b.want) {
			t.Errorf("call %d: %v want %v", i, b.got, b.want)
		}
		if seen[b.got] {
			t.Error("rejection errors are not distinct")
		}
		seen[b.got] = true
	}
	if err := a.Submit("after", 1, 1); err != nil {
		t.Fatalf("unusable after rejection: %v", err)
	}
	if got := a.Run(); !slices.Equal(got, []string{"ok", "after"}) {
		t.Errorf("state leaked from rejected submits: %v", got)
	}
}

// TestConcurrentSubmit: N goroutines submit distinct ids; after WaitGroup Run
// completes each exactly once. No sleeps.
func TestConcurrentSubmit(t *testing.T) {
	for _, N := range []int{50, 500} {
		a, wg := api.New(), sync.WaitGroup{}
		for i := 0; i < N; i++ {
			wg.Add(1)
			go func(i int) { defer wg.Done(); _ = a.Submit(fmt.Sprintf("g%d", i), 0, int64(i+1)) }(i)
		}
		wg.Wait()
		got, seen := a.Run(), map[string]bool{}
		for _, id := range got {
			seen[id] = true
		}
		if len(got) != N || len(seen) != N {
			t.Errorf("N=%d: %d jobs, %d distinct", N, len(got), len(seen))
		}
	}
}
