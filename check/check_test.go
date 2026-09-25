package check

import (
	"errors"
	"math"
	"testing"

	"ontology/arr"
	"ontology/rot"
)

func TestFixedCases(t *testing.T) {
	for _, c := range []fixedCase{
		{"hit", []int{4, 5, 6, 7, 0, 1, 2}, 0, 4}, {"miss", []int{4, 5, 6, 7, 0, 1, 2}, 3, -1},
		{"single hit", []int{9}, 9, 0}, {"single miss", []int{9}, 1, -1},
		{"empty", nil, 1, -1}, {"sorted", []int{0, 1, 2, 4, 5, 6, 7}, 5, 4},
	} {
		if got := rot.Search(c.nums, c.target); got != c.want {
			t.Fatalf("%s: %d", c.name, got)
		}
	}
}

func TestNaiveBinarySearchMissesRotatedTarget(t *testing.T) {
	nums := []int{4, 5, 6, 7, 0, 1, 2}
	if naiveBinarySearch(nums, 0) != -1 || rot.Search(nums, 0) != 4 {
		t.Fatal("naive/rot mismatch")
	}
}

func TestRandomMatchesLinearSearch(t *testing.T) {
	r := newDeterministicRandom()
	for range 10000 {
		nums, target := rotatedCase(r)
		if got, want := rot.Search(nums, target), LinearSearch(nums, target); got != want {
			t.Fatalf("nums=%v target=%d", nums, target)
		}
	}
}

func TestComparisonBound(t *testing.T) {
	nums := make([]int, 100000)
	for i := range nums {
		nums[i] = i
	}
	limit := int(2*math.Log2(float64(len(nums))) + 4)
	for p := 0; p < len(nums); p += 9973 {
		rotated := append(append([]int{}, nums[p:]...), nums[:p]...)
		for _, target := range []int{-1, 0, 50000, 99999, 100000} {
			if _, n := rot.SearchCount(rotated, target); n > limit {
				t.Fatalf("comparisons=%d", n)
			}
		}
	}
}

func TestValidationSentinelsAndConcurrentSearch(t *testing.T) {
	for _, c := range []errorCase{
		{"empty", nil, arr.ErrEmpty}, {"duplicate", []int{1, 1}, arr.ErrDuplicate},
		{"invalid", []int{3, 1, 2, 0}, arr.ErrNotRotatedSorted},
	} {
		if got := arr.Validate(c.nums); !errors.Is(got, c.err) {
			t.Fatalf("%s: %v", c.name, got)
		}
	}
	if got, err := arr.Search(nil, 1); got != -1 || err != nil {
		t.Fatal("empty search")
	}
	if !concurrentSearchWorks() {
		t.Fatal("concurrent search failed")
	}
}

func naiveBinarySearch(nums []int, target int) int {
	lo, hi := 0, len(nums)-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		if nums[mid] == target {
			return mid
		}
		if target < nums[mid] {
			hi = mid - 1
		} else {
			lo = mid + 1
		}
	}
	return -1
}
