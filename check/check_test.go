package check

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/arr"
	"ontology/rot"
)

func TestSearchTable(t *testing.T) {
	for _, c := range []struct {
		nums   []int
		target int
		want   int
	}{
		{[]int{4, 5, 6, 7, 0, 1, 2}, 0, 4},
		{[]int{4, 5, 6, 7, 0, 1, 2}, 3, -1},
		{[]int{1}, 1, 0}, {[]int{1}, 0, -1}, {nil, 5, -1},
		{[]int{1, 2, 3, 4}, 4, 3},
		{[]int{2, 1}, 1, 1}, {[]int{2, 1}, 3, -1},
	} {
		if got := rot.Search(c.nums, c.target); got != c.want {
			t.Errorf("Search(%v,%d)=%d want %d", c.nums, c.target, got, c.want)
		}
	}
}

func TestNaiveBinaryMisses(t *testing.T) {
	nums := []int{4, 5, 6, 7, 0, 1, 2}
	if NaiveBinary(nums, 0) != -1 || rot.Search(nums, 0) != 4 {
		t.Fatalf("NaiveBinary 必须漏查 0（返回 -1），rot.Search 必须返回 4")
	}
}

func TestSearchVsLinear(t *testing.T) {
	rng := rand.New(rand.NewSource(20260926))
	for i := 0; i < 10000; i++ {
		n := rng.Intn(64)
		nums := RotatedInts(n, rng.Intn(n+1))
		target := rng.Intn(n+64) - 32
		if n > 0 && rng.Intn(2) == 0 {
			target = nums[rng.Intn(n)]
		}
		if got, want := rot.Search(nums, target), Linear(nums, target); got != want {
			t.Fatalf("case %d: Search(%v,%d)=%d want %d", i, nums, target, got, want)
		}
	}
}

func TestComparisonBound(t *testing.T) {
	const n, k = 100000, 34567
	nums, bound := RotatedInts(n, k), 2*math.Log2(n)+4
	for _, target := range []int{0, k - 1, k, n - 1, -1, n + 5} {
		rot.ResetComparisons()
		rot.Search(nums, target)
		if c := float64(rot.Comparisons()); c > bound {
			t.Errorf("target=%d comparisons=%.0f > %.2f", target, c, bound)
		}
	}
}

func TestValidate(t *testing.T) {
	for _, c := range []struct {
		nums []int
		want error
	}{
		{nil, nil}, {[]int{1, 2, 3}, nil},
		{[]int{4, 5, 6, 7, 0, 1, 2}, nil},
		{[]int{1, 1}, arr.ErrNotStrict},
		{[]int{2, 1, 2, 1}, arr.ErrMultiPivot},
		{[]int{1, 2, 3, 0, 4}, arr.ErrBadRotation},
	} {
		if err := arr.Validate(c.nums); !errors.Is(err, c.want) {
			t.Errorf("Validate(%v)=%v want %v", c.nums, err, c.want)
		}
	}
}

func TestConcurrent(t *testing.T) {
	nums := RotatedInts(7, 4)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(seed)))
			for i := 0; i < 2000; i++ {
				target := rng.Intn(7)
				if got, want := rot.Search(nums, target), Linear(nums, target); got != want {
					t.Errorf("Search(%d)=%d want %d", target, got, want)
				}
			}
		}(g)
	}
	wg.Wait()
}
