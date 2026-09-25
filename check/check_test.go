package check_test

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/check"
	"ontology/ord"
	"ontology/part"
)

func TestConsistentWithNaive(t *testing.T) {
	cases := []struct {
		name  string
		arr   []int
		pivot int
	}{
		{"empty", nil, 1},
		{"single-less", []int{0}, 1},
		{"single-equal", []int{1}, 1},
		{"single-greater", []int{2}, 1},
		{"all-less", []int{-3, -1, -2, 0}, 1},
		{"all-equal", []int{1, 1, 1, 1, 1}, 1},
		{"all-greater", []int{5, 3, 4, 2}, 1},
		{"mixed", []int{2, 0, 2, 1, 1, 0}, 1},
	}
	for _, tc := range cases {
		if !check.Consistent(tc.arr, tc.pivot) {
			t.Errorf("%s: inconsistent with naive", tc.name)
		}
	}
	arr := []int{2, 0, 2, 1, 1, 0}
	lt, gt := part.ThreeWayPartition(arr, 1)
	if !slices.Equal(arr, []int{0, 0, 1, 1, 2, 2}) || lt != 2 || gt != 4 {
		t.Errorf("pinned: got arr=%v lt=%d gt=%d", arr, lt, gt)
	}
}

func TestSwapCounts(t *testing.T) {
	const n = 10000
	part.ResetSwaps()
	lt, gt := part.ThreeWayPartition(make([]int, n), 0)
	if lt != 0 || gt != n || part.Swaps() > n {
		t.Errorf("three-way: lt=%d gt=%d swaps=%d, n=%d", lt, gt, part.Swaps(), n)
	}
	swaps := 0
	twoWaySort(make([]int, n), &swaps)
	if swaps <= 100*n {
		t.Errorf("two-way: swaps=%d, want far above O(n)", swaps)
	}
}

// twoWaySort is the buggy two-way (Lomuto) quicksort from the incident:
// equal-to-pivot elements get swapped again at every recursion level.
func twoWaySort(a []int, swaps *int) {
	if len(a) < 2 {
		return
	}
	p, i := a[len(a)-1], 0
	for j := 0; j < len(a)-1; j++ {
		if a[j] <= p {
			a[i], a[j] = a[j], a[i]
			*swaps++
			i++
		}
	}
	a[i], a[len(a)-1] = a[len(a)-1], a[i]
	*swaps++
	twoWaySort(a[:i], swaps)
	twoWaySort(a[i+1:], swaps)
}

func TestSentinelErrors(t *testing.T) {
	cases := []struct{ err, want error }{
		{ord.Verify([]int{1}, 1, 2, 3), ord.ErrBadRange},
		{ord.Verify([]int{2, 1}, 1, 0, 1), ord.ErrWrongSegment},
		{ord.SameMultiset([]int{1, 2}, []int{1, 3}), ord.ErrLostElements},
	}
	for i, tc := range cases {
		if !errors.Is(tc.err, tc.want) {
			t.Errorf("case %d: err=%v, want %v", i, tc.err, tc.want)
		}
	}
}

func TestConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := ord.Checked([]int{g, 1, 0, 2, 1, g}, 1); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
