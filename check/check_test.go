package check_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/arr"
	"ontology/check"
	"ontology/kad"
)

var sumCases = []struct {
	name string
	arr  []int
	want int
}{
	{"all negative non-empty", []int{-2, -3, -1}, -1},
	{"single element", []int{7}, 7},
	{"all positive whole array", []int{1, 2, 3, 4}, 10},
	{"mixed", []int{-2, 1, -3, 4, -1, 2, 1, -5, 4}, 6},
}

func TestMaxSubarraySum(t *testing.T) {
	for _, tc := range sumCases {
		got, naive := kad.MaxSubarraySum(tc.arr), check.NaiveMaxSum(tc.arr)
		if got != tc.want || got != naive {
			t.Errorf("%s: kad = %d, want %d, naive %d", tc.name, got, tc.want, naive)
		}
	}
}

func TestClampToZeroIsWrong(t *testing.T) {
	wrong := func(a []int) (best int) { // clamp-to-0 form: allows empty subarray
		cur := 0
		for _, v := range a {
			if cur = max(cur+v, 0); cur > best {
				best = cur
			}
		}
		return best
	}
	if wrong([]int{-2, -3, -1}) != 0 || kad.MaxSubarraySum([]int{-2, -3, -1}) != -1 {
		t.Fatal("clamp-to-0 must give 0 (empty subarray); kad must give -1 (non-empty)")
	}
}

func TestMaxSubarrayRange(t *testing.T) {
	for _, tc := range sumCases {
		lo, hi, sum := kad.MaxSubarrayRange(tc.arr)
		if lo < 0 || hi >= len(tc.arr) || lo > hi {
			t.Fatalf("%s: invalid range [%d,%d]", tc.name, lo, hi)
		}
		got := 0
		for _, v := range tc.arr[lo : hi+1] {
			got += v
		}
		if naive := check.NaiveMaxSum(tc.arr); got != sum || sum != naive {
			t.Errorf("%s: range sum %d, recomputed %d, naive %d", tc.name, sum, got, naive)
		}
	}
}

func TestSentinelErrors(t *testing.T) {
	cases := []struct {
		name string
		arr  []int
		want error
	}{
		{"nil", nil, arr.ErrNil},
		{"empty", []int{}, arr.ErrEmpty},
		{"too large", make([]int, arr.MaxLen+1), arr.ErrTooLarge},
	}
	for _, tc := range cases {
		if _, err := arr.MaxSum(tc.arr); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want errors.Is %v", tc.name, err, tc.want)
		}
	}
}

func TestLinearAccesses(t *testing.T) {
	const n = 100000
	big, before := make([]int, n), kad.ElementAccesses()
	kad.MaxSubarraySum(big)
	if got := kad.ElementAccesses() - before; got > n {
		t.Errorf("element accesses = %d, want <= %d (single pass)", got, n)
	}
}

func TestConcurrentPure(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Go(func() {
			if kad.MaxSubarraySum([]int{-2, 1, -3, 4, -1, 2, 1, -5, 4}) != 6 {
				t.Error("concurrent result mismatch")
			}
		})
	}
	wg.Wait()
}
