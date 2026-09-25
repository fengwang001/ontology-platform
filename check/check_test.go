package check

import (
	"errors"
	"math"
	"math/rand/v2"
	"sync"
	"testing"

	"ontology/arr"
	"ontology/rot"
)

func eq(t *testing.T, msg string, got, want int) {
	if got != want {
		t.Errorf("%s: got %d want %d", msg, got, want)
	}
}

func errIs(t *testing.T, msg string, got, want error) {
	if !errors.Is(got, want) {
		t.Errorf("%s: got %v want %v", msg, got, want)
	}
}

func TestSearch(t *testing.T) {
	nums := []int{4, 5, 6, 7, 0, 1, 2}
	for _, c := range [][2]int{{0, 4}, {3, -1}, {7, 3}, {2, 6}, {4, 0}, {8, -1}} {
		eq(t, "pinned", rot.Search(nums, c[0]), c[1])
	}
	eq(t, "naive must miss", rot.NaiveSearch(nums, 0), -1)
	edgeNums := [][]int{{1}, {1}, nil, {1, 2, 3, 4, 5}, {1, 2, 3, 4, 5}, {3, 1}, {3, 1}}
	edgeTargets := []int{1, 2, 5, 4, 9, 1, 2}
	edgeWants := []int{0, -1, -1, 3, -1, 1, -1}
	for i := range edgeNums {
		eq(t, "edge", rot.Search(edgeNums[i], edgeTargets[i]), edgeWants[i])
	}
}

func TestDifferentialRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for iter := 0; iter < 10000; iter++ {
		n, v := rng.IntN(100), rng.IntN(50)-25
		a := make([]int, n)
		for j := range a {
			v += 1 + rng.IntN(3)
			a[j] = v
		}
		if n > 1 {
			k := rng.IntN(n)
			r := append([]int{}, a[k:]...)
			a = append(r, a[:k]...)
		}
		target := rng.IntN(60) - 30
		eq(t, "differential", rot.Search(a, target), LinearSearch(a, target))
	}
	const n = 100000
	big := make([]int, n)
	for i := range big {
		big[i] = i
	}
	big = append(append([]int{}, big[3719:]...), big[:3719]...)
	for _, target := range []int{0, 99999, -1, 100000} {
		rot.Search(big, target)
		if float64(rot.LastComparisons()) > 2*math.Log2(float64(n))+4 {
			t.Fatalf("target=%d exceeds comparison bound", target)
		}
	}
}

func TestArr(t *testing.T) {
	valNums := [][]int{nil, {}, {2, 2}, {1, 3, 2, 4}, {1}, {1, 2, 3}, {4, 5, 6, 7, 0, 1, 2}}
	valErrs := []error{arr.ErrEmpty, arr.ErrEmpty, arr.ErrDuplicate, arr.ErrNotRotated, nil, nil, nil}
	for i := range valNums {
		errIs(t, "validate", arr.Validate(valNums[i]), valErrs[i])
	}
	sNums := [][]int{nil, {2, 2}, {1, 3, 2}, {4, 5, 6, 7, 0, 1, 2}, {4, 5, 6, 7, 0, 1, 2}}
	sTargets, sWants, sErrs := []int{5, 2, 2, 0, 3}, []int{-1, -1, -1, 4, -1},
		[]error{arr.ErrEmpty, arr.ErrDuplicate, arr.ErrNotRotated, nil, nil}
	for i := range sNums {
		got, err := arr.Search(sNums[i], sTargets[i])
		eq(t, "arr.Search", got, sWants[i])
		errIs(t, "arr.Search", err, sErrs[i])
	}
}

func TestConcurrent(t *testing.T) {
	nums := []int{4, 5, 6, 7, 0, 1, 2}
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(target int) {
			defer wg.Done()
			if rot.Search(nums, target) != LinearSearch(nums, target) {
				t.Errorf("target=%d mismatch", target)
			}
		}(i)
	}
	wg.Wait()
}
