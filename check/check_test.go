package check_test

import (
	"errors"
	"fmt"
	"testing"

	"ontology/check"
	"ontology/rng"
	"ontology/shuffle"
)

func base(n int) []int {
	a := make([]int, n)
	for i := range a {
		a[i] = i
	}
	return a
}
func badShuffle(a []int, seed uint64) {
	// 内联事故实现：每步从全范围 [0, n) 选交换对象。
	src := rng.New(seed)
	for i := range a {
		j, _ := src.Intn(len(a))
		a[i], a[j] = a[j], a[i]
	}
}
func dist(n, trials int, wash func([]int, uint64)) (out []int) {
	counts := map[string]int{}
	for k := 0; k < trials; k++ {
		a := base(n)
		wash(a, uint64(k))
		counts[fmt.Sprint(a)]++
	}
	for _, c := range counts {
		out = append(out, c)
	}
	return out
}
func fatalf(t *testing.T, bad bool, format string, args ...any) {
	t.Helper()
	if bad {
		t.Fatalf(format, args...)
	}
}
func TestShuffle(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"deterministic permutation", func(t *testing.T) {
			for _, n := range []int{0, 1, 2, 5, 100} {
				a := base(n)
				shuffle.Shuffle(a, uint64(n))
				fatalf(t, check.VerifyPermutation(base(n), a) != nil, "n=%d not a permutation", n)
			}
			x, y := base(10), base(10)
			shuffle.Shuffle(x, 42)
			shuffle.Shuffle(y, 42)
			fatalf(t, fmt.Sprint(x) != fmt.Sprint(y), "same seed, different result")
		}},
		{"uniformity good vs buggy", func(t *testing.T) {
			counts := dist(4, 120000, shuffle.Shuffle[int])
			fatalf(t, len(counts) != 24, "%d permutations, want 24", len(counts))
			fatalf(t, check.VerifyUniform(counts, 1.5) != nil, "n=4 max/min ratio > 1.5")
			fatalf(t, check.MaxMinRatio(dist(2, 10000, shuffle.Shuffle[int])) > 1.2, "n=2 imbalanced")
			fatalf(t, check.MaxMinRatio(dist(7, 600000, badShuffle)) <= 5, "buggy full-range not skewed > 5")
		}},
		{"rand calls exactly n-1", func(t *testing.T) {
			for _, n := range []int{2, 3, 10, 100} {
				before := shuffle.RandCalls()
				shuffle.Shuffle(base(n), 7)
				fatalf(t, shuffle.RandCalls()-before != int64(n-1), "n=%d bad rng call count", n)
			}
		}},
		{"sentinel errors", func(t *testing.T) {
			_, e1 := rng.New(1).Intn(0)
			e2 := check.VerifyPermutation([]int{1, 2}, []int{1, 1})
			e3 := check.VerifyUniform([]int{1, 100}, 1.5)
			ok := errors.Is(e1, rng.ErrBadBound) && errors.Is(e2, check.ErrNotPermutation) && errors.Is(e3, check.ErrNonUniform)
			fatalf(t, !ok, "sentinels: %v %v %v", e1, e2, e3)
		}},
		{"concurrent independent", func(t *testing.T) {
			done := make(chan []int, 8)
			for w := uint64(0); w < 8; w++ {
				go func() {
					a := base(50)
					shuffle.Shuffle(a, w)
					done <- a
				}()
			}
			for i := 0; i < 8; i++ {
				fatalf(t, check.VerifyPermutation(base(50), <-done) != nil, "bad permutation")
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.run)
	}
}
