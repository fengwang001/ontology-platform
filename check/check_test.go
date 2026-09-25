package check

import (
	"errors"
	"math/rand/v2"
	"ontology/sel"
	"slices"
	"sync"
	"testing"
)

func TestKthSmallest(t *testing.T) {
	tests := []struct {
		arr     []int
		k, want int
	}{
		{[]int{3, 2, 1, 5, 4}, 2, 3}, // pinned
		{[]int{3, 2, 3, 1, 2}, 3, 3}, // pinned, duplicates
		{[]int{9, 4, 7, 1}, 0, 1},    // k=0 -> min
		{[]int{9, 4, 7, 1}, 3, 9},    // k=n-1 -> max
		{[]int{5, 5, 5, 5}, 2, 5},
	}
	for _, tt := range tests {
		if got, err := sel.KthSmallest(tt.arr, tt.k); err != nil || got != tt.want {
			t.Errorf("arr=%v k=%d: got %v,%v want %v", tt.arr, tt.k, got, err, tt.want)
		}
	}
	for _, n := range []int{1, 7, 100, 4096} { // vs sort reference
		arr := rand.Perm(n)
		got, _ := sel.KthSmallest(arr, n/2)
		if ref, _ := KthSmallestRef(slices.Clone(arr), n/2); got != ref {
			t.Errorf("n=%d: got %v, ref %v", n, got, ref)
		}
	}
}
func TestErrors(t *testing.T) {
	tests := []struct {
		arr  []int
		k    int
		want error
	}{
		{nil, 0, sel.ErrEmpty},
		{[]int{1}, -1, sel.ErrBadK},
		{[]int{1, 2}, 2, sel.ErrBadK},
	}
	for _, tt := range tests {
		if _, err := sel.KthSmallest(tt.arr, tt.k); !errors.Is(err, tt.want) {
			t.Errorf("got %v, want %v", err, tt.want)
		}
	}
}

// kthByValue is the buggy variant: picks the side by pivot VALUE vs k.
func kthByValue(arr []int, k int) int {
	lo, hi := 0, len(arr)-1
	for lo < hi {
		pivot, i := arr[hi], lo
		for j := lo; j < hi; j++ {
			if arr[j] < pivot {
				arr[i], arr[j] = arr[j], arr[i]
				i++
			}
		}
		arr[i], arr[hi] = arr[hi], arr[i]
		if pivot > k {
			hi = i - 1
		} else {
			lo = i + 1
		}
	}
	return arr[lo]
}
func TestValueCompareDirectionIsWrong(t *testing.T) {
	correct, _ := sel.KthSmallest([]int{3, 2, 1, 5, 4}, 2)
	if buggy := kthByValue([]int{3, 2, 1, 5, 4}, 2); buggy != 2 || correct != 3 {
		t.Fatalf("buggy=%d want 2, correct=%d want 3", buggy, correct)
	}
}
func TestComparisonBound(t *testing.T) {
	sel.ResetComparisons()
	for range 5 {
		_, _ = sel.KthSmallest(rand.Perm(100000), 50000)
	}
	if avg := sel.Comparisons() / 5; avg > 400000 {
		t.Fatalf("avg comparisons %d > 4n=400000", avg)
	}
}
func TestConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for range 50 {
				if got, _ := sel.KthSmallest(rand.Perm(256), g); got != g {
					t.Errorf("got %v, want %v", got, g)
				}
			}
		})
	}
	wg.Wait()
}
