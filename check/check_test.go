package check

import (
	"errors"
	"math/bits"
	"sync"
	"testing"

	"ontology/arr"
	"ontology/rot"
)

// naiveWrong：不判半边的错误普通二分，直接拿 nums[mid] 与 target 定方向。
func naiveWrong(a []int, x int) int {
	l, h := 0, len(a)-1
	for l <= h {
		m := int(uint(l+h) >> 1)
		switch {
		case a[m] == x:
			return m
		case a[m] < x:
			l = m + 1
		default:
			h = m - 1
		}
	}
	return -1
}
func TestSearchTable(t *testing.T) {
	for _, c := range []struct {
		a    []int
		x, w int
	}{
		{[]int{4, 5, 6, 7, 0, 1, 2}, 0, 4},
		{[]int{4, 5, 6, 7, 0, 1, 2}, 3, -1},
		{[]int{1}, 1, 0}, {[]int{1}, 2, -1},
		{[]int{1, 2, 3, 4, 5}, 4, 3}, {[]int{}, 1, -1},
		{[]int{4, 5, 6, 7, 0, 1, 2}, 2, 6},
	} {
		if g := rot.Search(c.a, c.x); g != c.w {
			t.Fatalf("got %d want %d", g, c.w)
		}
	}
}
func TestNaiveBisectionMisses(t *testing.T) {
	a := []int{4, 5, 6, 7, 0, 1, 2}
	if naiveWrong(a, 0) != -1 || rot.Search(a, 0) != 4 {
		t.Fatal("朴素实现必须漏查 0，正确实现应得下标 4")
	}
}
func TestEquivalentToLinear(t *testing.T) {
	var s uint64 = 1
	r := func() uint64 { s ^= s << 13; s ^= s >> 7; s ^= s << 17; return s }
	for i := 0; i < 10000; i++ {
		n := int(r()%32) + 1
		k := int(r() % uint64(n))
		a := make([]int, n)
		for j := range a {
			a[j] = ((j - k) + n) % n * 2
		}
		x := int(r()%uint64(2*n+2)) - 1
		if g, w := rot.Search(a, x), LinearSearch(a, x); g != w {
			t.Fatalf("#%d got %d want %d (%v,%d)", i, g, w, a, x)
		}
	}
}
func TestComparisonBound(t *testing.T) {
	const n = 100000
	bound := 2*bits.Len(uint(n-1)) + 4
	a := make([]int, n)
	for _, k := range []int{0, 1, n / 4, n / 2, 3 * n / 4, n - 1} {
		for i := range a {
			a[i] = (i - k + n) % n
		}
		for _, x := range []int{-1, 0, n/2 + k%2, n - 1, n} {
			if _, c := rot.SearchWithCount(a, x); c > bound {
				t.Fatalf("k=%d x=%d cmp=%d>%d", k, x, c, bound)
			}
		}
	}
}
func TestConcurrentPure(t *testing.T) {
	a := []int{4, 5, 6, 7, 0, 1, 2}
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			x := []int{0, 3, 4, 2}[g%4]
			if rot.Search(a, x) != LinearSearch(a, x) {
				t.Errorf("g %d mismatch", g)
			}
		}(g)
	}
	wg.Wait()
}
func TestArrErrors(t *testing.T) {
	for _, c := range [][2]any{
		{nil, arr.ErrNilInput},
		{[]int{2, 2, 3}, arr.ErrDuplicate},
		{[]int{1, 4, 2, 3, 0}, arr.ErrMalformed},
		{[]int{}, nil},
		{[]int{4, 5, 1, 2, 3}, nil},
	} {
		_, e := arr.Search(c[0].([]int), 1)
		w, _ := c[1].(error)
		if !errors.Is(e, w) {
			t.Fatalf("got %v want %v", e, w)
		}
	}
}
