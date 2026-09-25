package check_test

import (
	"errors"
	"math"
	"slices"
	"sync"
	"testing"

	"ontology/arr"
	"ontology/check"
	"ontology/cnt"
)

func TestCountInversions(t *testing.T) {
	cases := []struct {
		name string
		in   []int
		want int64
	}{
		{"空数组", []int{}, 0}, {"单元素", []int{7}, 0},
		{"已升序", []int{1, 2, 3, 4, 5}, 0}, {"完全降序", []int{5, 4, 3, 2, 1}, 10},
		{"含相等元素(稳定)", []int{2, 2, 1, 1}, 4}, {"走查样例", []int{2, 4, 1, 3, 5}, 3},
	}
	for _, c := range cases {
		got, err := cnt.CountInversions(c.in)
		if err != nil || got != c.want || got != check.NaiveCount(c.in) {
			t.Errorf("%s: got %d, err %v; want %d", c.name, got, err, c.want)
		}
	}
	if got := wrongCount([]int{2, 4, 1, 3, 5}); got == 3 {
		t.Fatal("错误实现也算出 3，无法与正确算法区分")
	}
}

// wrongCount 内联线上事故的错误实现：归并时累加「右半已取走数量」。
func wrongCount(a []int) int64 {
	if len(a) < 2 {
		return 0
	}
	m := len(a) / 2
	total := wrongCount(a[:m]) + wrongCount(a[m:])
	l, r := append([]int(nil), a[:m]...), append([]int(nil), a[m:]...)
	i, j := 0, 0
	for k := range a {
		if j >= len(r) || (i < len(l) && l[i] <= r[j]) {
			a[k], i = l[i], i+1
		} else {
			a[k], total, j = r[j], total+int64(j), j+1 // 错误：与逆序对无关
		}
	}
	return total
}

func TestLargeDescendingAndBound(t *testing.T) {
	n := 100000
	a := make([]int, n)
	for i := range a {
		a[i] = n - i
	}
	cnt.ResetComparisons()
	got, err := cnt.CountInversions(a)
	if want := int64(n) * int64(n-1) / 2; err != nil || got != want {
		t.Fatalf("got %d, err %v; want %d", got, err, want)
	}
	limit := int64(float64(n)*math.Log2(float64(n)) + float64(n))
	if got := cnt.Comparisons(); got > limit {
		t.Fatalf("比较次数 %d 超过上界 %d", got, limit)
	}
}

func TestConcurrentPure(t *testing.T) {
	in := []int{5, 1, 4, 2, 3} // 6 个逆序对
	before := slices.Clone(in)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				got, err := cnt.CountInversions(in)
				if err != nil || got != 6 {
					t.Errorf("got %d, err %v", got, err)
				}
			}
		})
	}
	wg.Wait()
	if !slices.Equal(in, before) {
		t.Fatal("输入被修改，不是纯函数")
	}
}

func TestArrSentinelErrors(t *testing.T) {
	ins := [][]int{nil, make([]int, arr.MaxN+1), {1, -2, 3}}
	wants := []error{arr.ErrNilSlice, arr.ErrOversize, arr.ErrNegative}
	for i := range ins {
		if _, err := arr.Count(ins[i]); !errors.Is(err, wants[i]) {
			t.Errorf("case %d: err %v, want %v", i, err, wants[i])
		}
	}
}
