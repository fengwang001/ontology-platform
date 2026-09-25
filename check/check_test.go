package check_test

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/check"
	"ontology/rng"
	"ontology/shuffle"
)

// broken 内联线上事故实现：每步从 [0, n) 全范围选，排列不均匀。
func broken(arr []int, seed uint64) {
	r := rng.New(seed)
	for i := range arr {
		j, _ := r.Intn(len(arr))
		arr[i], arr[j] = arr[j], arr[i]
	}
}

func base(n int) []int {
	a := make([]int, n)
	for i := range a {
		a[i] = i
	}
	return a
}

// TestSemantics 覆盖：确定性、多集置换、空/单元素原样、n 元恰好 n-1 次随机。
func TestSemantics(t *testing.T) {
	for _, n := range []int{0, 1, 2, 4, 10, 17} {
		a, b := base(n), base(n)
		shuffle.ResetRandomCalls()
		shuffle.Shuffle(a, 7)
		if got, want := shuffle.RandomCalls(), int64(max(n-1, 0)); got != want {
			t.Errorf("n=%d: 随机调用 %d 次, 期望 %d", n, got, want)
		}
		shuffle.Shuffle(b, 7)
		if !slices.Equal(a, b) {
			t.Errorf("n=%d: 同 seed 结果不一致", n)
		}
		if !check.IsPermutation(base(n), a) {
			t.Errorf("n=%d: 洗牌后不是多集置换", n)
		}
		if n < 2 && !slices.Equal(a, base(n)) {
			t.Errorf("n=%d: 空/单元素应原样返回", n)
		}
	}
}

// TestUniformity 钉住均匀性：正确实现计数接近均匀；全范围选极不均匀。
func TestUniformity(t *testing.T) {
	cases := []struct {
		name             string
		n, trials        int
		shuf             check.ShuffleFunc
		perms            int
		ratioLo, ratioHi float64
	}{
		{"n=2 各约50%", 2, 100000, shuffle.Shuffle[int], 2, 0, 1.22},
		{"n=4 均匀", 4, 120000, shuffle.Shuffle[int], 24, 0, 1.5},
		{"全范围选极不均匀", 7, 1000000, broken, 5040, 5, 1e9},
	}
	for _, c := range cases {
		counts, _ := check.Distribution(c.n, c.trials, c.shuf)
		if len(counts) != c.perms {
			t.Errorf("%s: 排列种类 %d, 期望 %d", c.name, len(counts), c.perms)
		}
		if ratio, _ := check.MaxMinRatio(counts); ratio < c.ratioLo || ratio > c.ratioHi {
			t.Errorf("%s: max/min=%.2f 期望(%.0f,%.0f)", c.name, ratio, c.ratioLo, c.ratioHi)
		}
	}
}

func TestConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			for k := 0; k < 500; k++ {
				shuffle.Shuffle(base(9), seed+uint64(k))
			}
		}(uint64(g * 1000))
	}
	wg.Wait()
}

func TestSentinelErrors(t *testing.T) {
	_, e1 := rng.New(1).Intn(0)
	_, e2 := check.Distribution(0, 1, broken)
	_, e3 := check.MaxMinRatio(nil)
	for i, c := range [][2]error{{e1, rng.ErrBadBound}, {e2, check.ErrBadTrials}, {e3, check.ErrNoCounts}} {
		if !errors.Is(c[0], c[1]) {
			t.Errorf("case %d: errors.Is(%v, %v) 不成立", i, c[0], c[1])
		}
	}
}
