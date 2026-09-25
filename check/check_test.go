package check_test

import (
	"errors"
	"math/rand/v2"
	"sync"
	"testing"

	"ontology/check"
	"ontology/ord"
	"ontology/sel"
)

func TestKthSmallest(t *testing.T) {
	cases := []struct {
		name    string
		arr     []int
		k, want int
	}{
		{"middle", []int{3, 2, 1, 5, 4}, 2, 3}, {"duplicates", []int{3, 2, 3, 1, 2}, 3, 3},
		{"min", []int{3, 2, 1, 5, 4}, 0, 1}, {"max", []int{3, 2, 1, 5, 4}, 4, 5},
		{"all equal", []int{2, 2, 2}, 1, 2}, {"reversed", []int{9, 7, 5, 3, 1}, 3, 7},
	}
	for _, tc := range cases {
		got, err := sel.KthSmallest(append([]int(nil), tc.arr...), tc.k)
		if ref := check.KthSmallestRef(tc.arr, tc.k); err != nil || got != tc.want || got != ref {
			t.Errorf("%s: got %v,%v want %v ref %v", tc.name, got, err, tc.want, ref)
		}
	}
}

func TestErrors(t *testing.T) {
	arrs := [][]int{nil, {1}, {1, 2}}
	ks := []int{0, -1, 2}
	wants := []error{ord.ErrEmpty, ord.ErrBadK, ord.ErrBadK}
	for i := range arrs {
		if _, err := sel.KthSmallest(arrs[i], ks[i]); !errors.Is(err, wants[i]) {
			t.Errorf("case %d: got %v want %v", i, err, wants[i])
		}
	}
}

func badKth(arr []int, k int) int { // 错误示范：用 pivot 的"值"与 k 比较方向（k 是下标不是值）
	lo, hi := 0, len(arr)-1
	for lo <= hi {
		i := lo
		for j := lo; j < hi; j++ {
			if arr[j] < arr[hi] {
				arr[i], arr[j] = arr[j], arr[i]
				i++
			}
		}
		arr[i], arr[hi] = arr[hi], arr[i]
		if arr[i] == k {
			return arr[i]
		} else if arr[i] > k {
			hi = i - 1
		} else {
			lo = i + 1
		}
	}
	return -1
}

func TestValueCompareIsWrong(t *testing.T) {
	if got := badKth([]int{3, 2, 1, 5, 4}, 2); got == 3 {
		t.Fatal("value-compare impl returned the correct answer 3")
	}
}

func TestCompareBound(t *testing.T) {
	const n, trials = 100000, 8
	var total int64
	for range trials {
		sel.ResetCompares()
		if _, err := sel.KthSmallest(rand.Perm(n), n/2); err != nil {
			t.Fatal(err)
		}
		total += sel.Compares()
	}
	if avg := total / trials; avg > 4*n {
		t.Fatalf("avg compares %d > 4n=%d", avg, 4*n)
	}
}

func TestConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for g := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, err := sel.KthSmallest([]int{5, 1, 4, 2, 3}, g%5); err != nil || got != g%5+1 {
				t.Errorf("g=%d got %v,%v", g, got, err)
			}
		}()
	}
	wg.Wait()
}
