package check

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/ord"
	"ontology/sel"
)

type kase struct {
	name    string
	arr     []int
	k, want int
	err     error
}

func TestKthSmallest(t *testing.T) {
	tests := []kase{
		{"pinned distinct", []int{3, 2, 1, 5, 4}, 2, 3, nil},
		{"pinned duplicates", []int{3, 2, 3, 1, 2}, 3, 3, nil},
		{"k=0 is min", []int{9, 4, 7, 1}, 0, 1, nil},
		{"k=n-1 is max", []int{9, 4, 7, 1}, 3, 9, nil},
		{"all equal", []int{2, 2, 2}, 1, 2, nil},
		{"empty", nil, 0, 0, ord.ErrEmpty},
		{"k too large", []int{1, 2}, 2, 0, ord.ErrBadK},
	}
	for _, tt := range tests {
		got, err := sel.KthSmallest(slices.Clone(tt.arr), tt.k)
		if tt.err != nil {
			if !errors.Is(err, tt.err) {
				t.Fatalf("%s: got err %v, want %v", tt.name, err, tt.err)
			}
			continue
		}
		if err != nil || got != tt.want || got != KthRef(tt.arr, tt.k) {
			t.Fatalf("%s: got %v, %v; want %d", tt.name, got, err, tt.want)
		}
	}
}

// badKth picks the recursion side by pivot VALUE vs k (wrong: k is an index).
func badKth(a []int, k int) int {
	if len(a) == 0 {
		return -1
	}
	p := 0
	for i := 1; i < len(a); i++ {
		if a[i] < a[0] {
			p++
			a[p], a[i] = a[i], a[p]
		}
	}
	a[0], a[p] = a[p], a[0]
	if p == k {
		return a[p]
	}
	if a[p] <= k { // BUG: compares pivot value with index k
		return badKth(a[p+1:], k-p-1)
	}
	return badKth(a[:p], k)
}

func TestValueBasedDirectionIsWrong(t *testing.T) {
	if got := badKth([]int{10, 20, 30}, 1); got == 20 {
		t.Fatal("value-based direction returned the correct kth element")
	}
}

func TestComparisonBound(t *testing.T) {
	const n, trials = 100000, 10
	arr := make([]int, n)
	for i := range arr {
		arr[i] = (i * 7919) % n
	}
	sel.ResetComparisons()
	for range trials {
		_, _ = sel.KthSmallest(slices.Clone(arr), n/2)
	}
	if got := sel.Comparisons(); got > trials*4*n {
		t.Fatalf("avg comparisons %d exceeds 4n=%d", got/trials, 4*n)
	}
}

func TestConcurrent(t *testing.T) {
	base := []int{9, 1, 8, 2, 7, 3, 6, 4, 5, 0}
	var wg sync.WaitGroup
	for k := range len(base) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, err := sel.KthSmallest(slices.Clone(base), k); err != nil || got != KthRef(base, k) {
				t.Errorf("k=%d: got %v, %v", k, got, err)
			}
		}()
	}
	wg.Wait()
}
