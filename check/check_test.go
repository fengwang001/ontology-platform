package check_test

import (
	"errors"
	"math/rand"
	"ontology/arr"
	"ontology/check"
	"ontology/rot"
	"sync"
	"testing"
)

func TestSearchTable(t *testing.T) {
	cases := []struct {
		nums         []int
		target, want int
	}{
		{[]int{4, 5, 6, 7, 0, 1, 2}, 0, 4}, {[]int{4, 5, 6, 7, 0, 1, 2}, 3, -1}, {nil, 9, -1}, // 钉住命中；未命中；空
		{[]int{1}, 1, 0}, {[]int{1}, 0, -1}, {[]int{0, 1, 2, 4, 5, 6, 7}, 5, 4}, // 单元素；无旋转
	}
	for _, tc := range cases {
		if got := rot.Search(tc.nums, tc.target); got != tc.want {
			t.Errorf("Search(%v,%d)=%d want %d", tc.nums, tc.target, got, tc.want)
		}
	}
}

func TestAgainstLinear(t *testing.T) { // 10000 组随机旋转点、随机 target 对拍
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 10000; i++ {
		nums := make([]int, 1+r.Intn(50))
		for j := range nums {
			nums[j] = j + 7
		}
		if k := r.Intn(len(nums)); k > 0 {
			nums = append(nums[len(nums)-k:], nums[:len(nums)-k]...)
		}
		target := r.Intn(len(nums) + 10)
		got, want := rot.Search(nums, target), check.Linear(nums, target)
		if (got == -1) != (want == -1) || (got >= 0 && nums[got] != target) {
			t.Fatalf("nums=%v target=%d: got %d, linear %d", nums, target, got, want)
		}
	}
}

func TestNaiveBinaryMisses(t *testing.T) { // 反例：不判哪半有序的普通二分
	nums := []int{4, 5, 6, 7, 0, 1, 2}
	lo, hi := 0, len(nums)-1
	for lo < hi { // 下界式普通二分：只比 nums[mid] 与 0
		if mid := (lo + hi) / 2; nums[mid] >= 0 {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	if nums[lo] == 0 || rot.Search(nums, 0) != 4 {
		t.Fatal("普通二分漏查 target=0；rot.Search 应命中下标 4")
	}
}

func TestComparisonBound(t *testing.T) {
	nums := make([]int, 100000)
	for i := range nums {
		nums[i] = (i + 377) % 100000
	}
	for _, target := range []int{0, 50000, 99999, 100001} {
		rot.ResetComparisons()
		rot.Search(nums, target)
		if c := rot.Comparisons(); c > 37 { // 上界 2*log2(100000)+4 ≈ 37.2
			t.Fatalf("target=%d: %d 次比较 > 上界 37", target, c)
		}
	}
}

func TestConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for range 1000 {
				rot.Search([]int{4, 5, 6, 7, 0, 1, 2}, g)
			}
		})
	}
	wg.Wait()
}

func TestArrValidate(t *testing.T) {
	cases := []struct {
		nums []int
		want error
	}{
		{nil, arr.ErrEmpty}, {[]int{1, 1, 2}, arr.ErrDuplicate}, {[]int{2, 1, 3}, arr.ErrNotRotation},
		{[]int{2, 1, 4, 3}, arr.ErrNotRotation}, {[]int{4, 5, 0, 1}, nil}, {[]int{1, 2, 3}, nil},
	}
	for _, tc := range cases {
		if err := arr.Validate(tc.nums); !errors.Is(err, tc.want) {
			t.Errorf("Validate(%v)=%v want %v", tc.nums, err, tc.want)
		}
	}
}
