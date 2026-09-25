package check_test

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"ontology/check"
	"ontology/rng"
	"ontology/shuffle"
)

// badShuffle 内联事故中的错误实现：循环结构相同，但每步从全范围 [0, n) 选。
func badShuffle(arr []int, seed uint64) {
	s := rng.New(seed)
	for i := 0; i < len(arr)-1; i++ {
		j, _ := s.Intn(len(arr))
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

func TestShuffleSemantics(t *testing.T) {
	for n, seed := range map[int]uint64{0: 1, 1: 1, 2: 1, 4: 7, 10: 42, 17: 3} {
		a, b := base(n), base(n)
		before := shuffle.RandCalls()
		if err := shuffle.Shuffle(a, seed); err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if got := shuffle.RandCalls() - before; got != int64(max(n-1, 0)) {
			t.Errorf("n=%d: 随机调用 %d 次, 应恰好 %d 次", n, got, max(n-1, 0))
		}
		if err := shuffle.Shuffle(b, seed); err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		sorted := slices.Sorted(slices.Values(a))
		if !slices.Equal(a, b) || !slices.Equal(sorted, base(n)) {
			t.Errorf("n=%d: 确定性或多集置换不成立", n)
		}
	}
}

func TestSentinelErrors(t *testing.T) {
	_, intnErr := rng.New(1).Intn(0)
	for name, pair := range map[string][2]error{
		"nil 切片": {shuffle.Shuffle([]int(nil), 1), shuffle.ErrNilSlice},
		"非正上界":   {intnErr, rng.ErrNonPositiveN},
		"不均匀":    {check.VerifyUniform(4, 2000, 1, 1.0), check.ErrNonUniform},
	} {
		if !errors.Is(pair[0], pair[1]) {
			t.Errorf("%s: errors.Is 应匹配 %v, 得到 %v", name, pair[1], pair[0])
		}
	}
}

// 均匀性：正确实现 max/min 不超过 hi；错误实现超过 lo。
func TestUniformity(t *testing.T) {
	good := func(a []int, s uint64) { _ = shuffle.Shuffle(a, s) }
	for _, c := range []struct {
		name     string
		shuf     func([]int, uint64)
		n, perms int
		lo, hi   float64
	}{
		{"n=4 均匀", good, 4, 24, 0, 1.5},
		{"n=2 各半", good, 2, 2, 0, 1.2},
		{"全范围错误实现", badShuffle, 5, 120, 5, 1e9},
	} {
		counts := map[string]int{}
		for i := 0; i < 120000; i++ {
			arr := base(c.n)
			c.shuf(arr, uint64(i))
			counts[fmt.Sprint(arr)]++
		}
		if r := check.MaxMinRatio(counts); len(counts) != c.perms || r <= c.lo || r > c.hi {
			t.Errorf("%s: 种数=%d max/min=%.2f", c.name, len(counts), r)
		}
	}
}

func TestConcurrentDistinctArrays(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Go(func() {
			for i := 0; i < 200; i++ {
				_ = shuffle.Shuffle(base(5), uint64(g))
			}
		})
	}
	wg.Wait()
}
