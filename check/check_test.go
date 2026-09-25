package check_test

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/check"
	"ontology/ord"
	"ontology/part"
)

func TestThreeWayPartition(t *testing.T) {
	cases := []struct {
		name     string
		in, want []int
	}{
		{"empty", nil, nil},
		{"single", []int{1}, nil},
		{"all-lt", []int{-2, -1, 0}, nil},
		{"all-eq", []int{1, 1, 1, 1}, nil},
		{"all-gt", []int{3, 2, 4}, nil},
		{"pinned", []int{2, 0, 2, 1, 1, 0}, []int{0, 0, 1, 1, 2, 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wlt, wgt := check.Naive(slices.Clone(tc.in), 1)
			got := slices.Clone(tc.in)
			lt, gt := part.ThreeWayPartition(got, 1)
			switch {
			case lt != wlt || gt != wgt:
				t.Fatalf("区间=(%d,%d)，参照=(%d,%d)", lt, gt, wlt, wgt)
			case ord.Verify(got, 1, lt, gt) != nil:
				t.Fatalf("三段不变量被破坏: %v", got)
			case !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(tc.in))):
				t.Fatalf("多重集不一致: got=%v in=%v", got, tc.in)
			case tc.want != nil && !slices.Equal(got, tc.want):
				t.Fatalf("精确结果=%v，期望=%v", got, tc.want)
			}
		})
	}
}
func TestComplexityBounds(t *testing.T) {
	const n = 10000
	before := part.Swaps()
	lt, gt := part.ThreeWayPartition(make([]int, n), 0) // 全等于 pivot
	if lt != 0 || gt != n {
		t.Fatalf("全等于应得区间 (0,%d)，得 (%d,%d)", n, lt, gt)
	}
	if d := part.Swaps() - before; d > n {
		t.Fatalf("三路交换 %d 次，超过上界 n=%d", d, n)
	}
	if s := twoWayBad(make([]int, n), 0); s <= n*n/4 {
		t.Fatalf("两路交换 %d 次，未远超 O(n)", s)
	}
}
func twoWayBad(a []int, pivot int) (swaps int) { // 两路分区的错误实现：==pivot 元素逐个冒泡进尾部等于区
	for k, end := 0, len(a); k < end; k++ {
		if a[k] == pivot {
			for j := k; j < end-1; j++ {
				a[j], a[j+1] = a[j+1], a[j]
				swaps++
			}
			end--
			k--
		}
	}
	return swaps
}
func TestSentinelErrors(t *testing.T) {
	cases := []struct {
		arr  []int
		want error
	}{
		{[]int{5, 1, 9}, ord.ErrLowerSegment},
		{[]int{0, 2, 9}, ord.ErrEqualSegment},
		{[]int{0, 1, 0}, ord.ErrUpperSegment},
	}
	for _, tc := range cases {
		if err := ord.Verify(tc.arr, 1, 1, 2); !errors.Is(err, tc.want) {
			t.Errorf("Verify(%v) err=%v，期望 errors.Is %v", tc.arr, err, tc.want)
		}
	}
}
func TestConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := []int{3, 1, 2, 1, 0, 2, 1}
			if lt, gt := part.ThreeWayPartition(a, 1); ord.Verify(a, 1, lt, gt) != nil {
				t.Errorf("并发结果错误: %v", a)
			}
		}()
	}
	wg.Wait()
}
