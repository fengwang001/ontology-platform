package check

import (
	"errors"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"

	"ontology/sel"
)

func TestKthSmallest(t *testing.T) {
	for _, tc := range []struct {
		name string
		arr  []int
		k    int
		want int
		err  error
	}{
		{"middle", []int{3, 2, 1, 5, 4}, 2, 3, nil},
		{"duplicates", []int{3, 2, 3, 1, 2}, 3, 3, nil},
		{"minimum", []int{5, 1, 4}, 0, 1, nil},
		{"maximum", []int{5, 1, 4}, 2, 5, nil},
		{"all equal", []int{7, 7, 7}, 1, 7, nil},
		{"empty", nil, 0, 0, sel.ErrEmpty},
		{"negative k", []int{1}, -1, 0, sel.ErrBadK},
		{"k equals length", []int{1}, 1, 0, sel.ErrBadK},
	} {
		got, err := sel.KthSmallest(slices.Clone(tc.arr), tc.k)
		want, wantErr := NaiveKthSmallest(tc.arr, tc.k)
		if !errors.Is(err, tc.err) || !errors.Is(err, wantErr) || tc.err == nil && (got != tc.want || got != want) {
			t.Fatalf("%s: got (%v,%v), want (%v,%v), ref (%v,%v)", tc.name, got, err, tc.want, tc.err, want, wantErr)
		}
	}
}
func TestValueDirectionIsWrong(t *testing.T) {
	if got := valueDirectionKth(slices.Clone([]int{3, 2, 1, 5, 4}), 2); got != 2 {
		t.Fatalf("value-based direction got %d, want wrong element 2", got)
	}
}

func TestAverageComparisons(t *testing.T) {
	const n, trials, bound = 100000, 20, 400000
	for trial := range trials {
		arr := make([]int, n)
		for i := range arr {
			arr[i] = int(rand.Uint64())
		}
		got, comparisons, err := sel.SelectWithCount(slices.Clone(arr), n/2)
		want, _ := NaiveKthSmallest(arr, n/2)
		if err != nil || comparisons > bound || got != want {
			t.Fatalf("trial %d: got %d want %d comparisons %d bound %d err %v", trial, got, want, comparisons, bound, err)
		}
	}
}

func TestConcurrentCalls(t *testing.T) {
	var wg sync.WaitGroup
	cases := [][2]int{{2, 0}, {3, 1}}
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, pair := range cases {
				arr := [][]int{{3, 2, 1, 5, 4}, {3, 2, 3, 1, 2}}[pair[1]]
				if _, err := sel.KthSmallest(slices.Clone(arr), pair[0]); err != nil {
					t.Error(err)
				}
				if _, err := sel.KthSmallest([]int(nil), 0); !errors.Is(err, sel.ErrEmpty) {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
}
func valueDirectionKth(arr []int, k int) int {
	lo, hi := 0, len(arr)-1
	for lo < hi {
		pivot, store := arr[hi], lo
		for i := lo; i < hi; i++ {
			if arr[i] < pivot {
				arr[store], arr[i] = arr[i], arr[store]
				store++
			}
		}
		arr[store], arr[hi] = arr[hi], arr[store]
		if pivot == k {
			return pivot
		}
		if pivot > k {
			hi = store - 1
		} else {
			lo = store + 1
		}
	}
	return arr[lo]
}
