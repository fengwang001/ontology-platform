package check_test

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"ontology/check"
	"ontology/rng"
	"ontology/shuffle"
)

func gather(n, trials int, shuf func([]int, uint64)) []int64 {
	counts := map[string]int64{}
	for t := 0; t < trials; t++ {
		a := make([]int, n)
		for i := range a {
			a[i] = i
		}
		shuf(a, uint64(t))
		counts[fmt.Sprint(a)]++
	}
	out := []int64{}
	for _, c := range counts {
		out = append(out, c)
	}
	return out
}

func fair(a []int, s uint64) { _ = shuffle.Shuffle(a, s) }

func naive(a []int, s uint64) {
	g := rng.New(s)
	for i := 0; i < len(a)-1; i++ {
		j, _ := g.Intn(len(a))
		a[i], a[j] = a[j], a[i]
	}
}

func TestShuffle(t *testing.T) {
	for _, tc := range []struct{ n, seed int }{{0, 1}, {1, 1}, {2, 7}, {10, 42}} {
		a := make([]int, tc.n)
		for i := range a {
			a[i] = i
		}
		orig, b := slices.Clone(a), slices.Clone(a)
		errA, errB := shuffle.Shuffle(a, uint64(tc.seed)), shuffle.Shuffle(b, uint64(tc.seed))
		if errA != nil || errB != nil {
			t.Fatal(errA, errB)
		}
		if !slices.Equal(a, b) {
			t.Fatalf("n=%d not deterministic", tc.n)
		}
		slices.Sort(b)
		if !slices.Equal(b, orig) {
			t.Fatalf("n=%d not a permutation: %v", tc.n, a)
		}
		if got, want := shuffle.RandCalls(), int64(max(tc.n-1, 0)); got != want {
			t.Fatalf("n=%d rand calls = %d, want %d", tc.n, got, want)
		}
	}
}

func TestUniformity(t *testing.T) {
	for _, tc := range []struct{ n, trials, perms int }{{4, 120000, 24}, {2, 10000, 2}} {
		counts := gather(tc.n, tc.trials, fair)
		if len(counts) != tc.perms {
			t.Fatalf("n=%d perms = %d, want %d", tc.n, len(counts), tc.perms)
		}
		if ratio, _ := check.MaxMinRatio(counts); ratio > 1.5 {
			t.Fatalf("n=%d ratio = %v", tc.n, ratio)
		}
	}
	counts := gather(5, 120000, naive)
	if len(counts) != 120 {
		t.Fatalf("naive perms = %d", len(counts))
	}
	if ratio, _ := check.MaxMinRatio(counts); ratio <= 5 {
		t.Fatalf("naive ratio = %v, want > 5", ratio)
	}
}

func TestErrorsAndConcurrent(t *testing.T) {
	_, e1 := rng.New(1).Intn(0)
	e2 := shuffle.Shuffle[int](nil, 1)
	_, e3 := check.MaxMinRatio(nil)
	for i, tc := range [][2]error{{e1, rng.ErrNonPositiveN}, {e2, shuffle.ErrNilSlice}, {e3, check.ErrEmptyCounts}} {
		if !errors.Is(tc[0], tc[1]) {
			t.Fatalf("case %d: %v", i, tc[0])
		}
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(s uint64) { defer wg.Done(); fair([]int{0, 1, 2, 3, 4, 5}, s) }(uint64(w))
	}
	wg.Wait()
}
