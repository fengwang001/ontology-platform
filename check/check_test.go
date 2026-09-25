package check_test

import (
	"errors"
	"math/rand/v2"
	"sync"
	"testing"

	ref "ontology/check"
	"ontology/sel"
)

type semanticCase struct {
	name    string
	arr     []int
	k, want int
	err     error
}
type referenceCase struct {
	name string
	arr  []int
	k    int
}

func TestKthSmallestSemantics(t *testing.T) {
	cases := []semanticCase{
		{"middle", []int{3, 2, 1, 5, 4}, 2, 3, nil},
		{"duplicates", []int{3, 2, 3, 1, 2}, 3, 3, nil},
		{"minimum", []int{5, 2, 8}, 0, 2, nil},
		{"maximum", []int{5, 2, 8}, 2, 8, nil},
		{"empty", []int{}, 0, 0, sel.ErrEmpty},
		{"negative-k", []int{1}, -1, 0, sel.ErrBadK},
		{"large-k", []int{1}, 1, 0, sel.ErrBadK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sel.KthSmallest(append([]int(nil), tc.arr...), tc.k)
			if !errors.Is(err, tc.err) || got != tc.want {
				t.Fatalf("got (%d,%v), want (%d,%v)", got, err, tc.want, tc.err)
			}
		})
	}
}
func TestWrongValueDirectionFails(t *testing.T) {
	if got := kthByWrongValueDirection([]int{3, 2, 1, 5, 4}, 2); got == 3 {
		t.Fatal("wrong value-direction rule returned 3")
	}
}
func TestSortedReferenceMatches(t *testing.T) {
	cases := []referenceCase{
		{"random", rand.Perm(100), 37},
		{"duplicates", []int{3, 2, 3, 1, 2, 2, 3}, 4},
		{"one", []int{42}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := ref.KthSmallestSorted(tc.arr, tc.k)
			got, gotErr := sel.KthSmallest(append([]int(nil), tc.arr...), tc.k)
			if err != nil || gotErr != nil || got != want {
				t.Fatalf("got (%d,%v), want (%d,%v)", got, gotErr, want, err)
			}
		})
	}
}
func TestAverageComparisonBoundAndConcurrent(t *testing.T) {
	const n, runs = 100000, 10
	var wg sync.WaitGroup
	totals := make([]int, runs)
	for i := range runs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, totals[i], _ = sel.KthSmallestWithStats(rand.Perm(n), n/2)
		}(i)
	}
	wg.Wait()
	var total int
	for _, c := range totals {
		total += c
	}
	if total/runs > 4*n {
		t.Fatalf("average comparisons = %d, limit %d", total/runs, 4*n)
	}
}
func kthByWrongValueDirection(arr []int, k int) int {
	work := append([]int(nil), arr...)
	low, high := 0, len(work)-1
	for low < high {
		pivot, p := work[high], low
		for i := low; i < high; i++ {
			if work[i] < pivot {
				work[i], work[p], p = work[p], work[i], p+1
			}
		}
		work[p], work[high] = work[high], work[p]
		if pivot < k {
			low = p + 1
		} else {
			high = p - 1
		}
	}
	return work[low]
}
