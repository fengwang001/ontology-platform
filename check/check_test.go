package check

import (
	"errors"
	"math"
	"math/rand"
	"slices"
	"testing"

	"ontology/arr"
	"ontology/rot"
)

func linearSearch(nums []int, target int) int {
	return slices.Index(nums, target)
}

func naiveBinarySearch(nums []int, target int) int {
	lo, hi := 0, len(nums)-1
	for lo <= hi {
		mid := int(uint(lo+hi) >> 1)
		if nums[mid] == target {
			return mid
		}
		if target < nums[mid] {
			hi = mid - 1
			continue
		}
		lo = mid + 1
	}
	return -1
}

func TestSearchCases(t *testing.T) {
	cases := [][3][]int{
		{{4, 5, 6, 7, 0, 1, 2}, {0}, {4}},
		{{4, 5, 6, 7, 0, 1, 2}, {3}, {-1}},
		{nil, {1}, {-1}},
		{{1}, {1}, {0}},
		{{1}, {2}, {-1}},
		{{0, 1, 2, 3, 4}, {3}, {3}},
		{{0, 1, 2, 3, 4}, {8}, {-1}},
	}
	for _, c := range cases {
		if got := rot.Search(c[0], c[1][0]); got != c[2][0] {
			t.Fatalf("got %d, want %d", got, c[2][0])
		}
	}
}

func TestNaiveBinarySearchMissesRotatedTarget(t *testing.T) {
	cases := [][2]int{{0, -1}}
	for _, c := range cases {
		if got := naiveBinarySearch([]int{4, 5, 6, 7, 0, 1, 2}, c[0]); got != c[1] {
			t.Fatalf("got %d, want %d", got, c[1])
		}
	}
}

func TestSearchMatchesLinearReference(t *testing.T) {
	random := rand.New(rand.NewSource(1))
	for range 10000 {
		size, start := random.Intn(128)+1, random.Intn(1000)
		nums := make([]int, size)
		for i := range nums {
			nums[i] = start + i*2
		}
		pivot := random.Intn(size)
		left, right := append([]int{}, nums[pivot:]...), nums[:pivot]
		nums = append(left, right...)
		target := nums[random.Intn(size)]
		if random.Intn(2) == 1 { target = start - 1 - random.Intn(5) }
		if got, want := rot.Search(nums, target), linearSearch(nums, target); got != want {
			t.Fatalf("got %d, want %d", got, want)
		}
	}
}

func TestComparisonBound(t *testing.T) {
	nums, limit := make([]int, 100000), 2*math.Log2(100000)+4
	for target := -1; target < len(nums); target++ {
		if _, n := rot.SearchWithComparisons(nums, target); float64(n) > limit {
			t.Fatalf("comparisons %d, limit %.2f", n, limit)
		}
		nums[target+1] = target + 1
	}
}

func TestArrValidation(t *testing.T) {
	cases := [][3]any{
		{"empty", []int(nil), arr.ErrEmpty},
		{"duplicate", []int{1, 1}, arr.ErrDuplicate},
		{"not rotated", []int{3, 1, 2, 0}, arr.ErrNotRotated},
		{"valid", []int{4, 5, 6, 0, 1, 2}, nil},
	}
	for _, c := range cases {
		if err := arr.Validate(c[1].([]int)); !errors.Is(err, c[2].(error)) {
			t.Fatalf("%s: got %v, want %v", c[0], err, c[2])
		}
	}
}
