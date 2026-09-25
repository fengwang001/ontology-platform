// Command demo 运行 Fisher-Yates 洗牌的正确性判定，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"

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

func dist(n, trials int, wash func([]int, uint64)) []int {
	counts := map[string]int{}
	for k := 0; k < trials; k++ {
		a := base(n)
		wash(a, uint64(k))
		counts[fmt.Sprint(a)]++
	}
	out := []int{}
	for _, c := range counts {
		out = append(out, c)
	}
	return out
}

func main() {
	pass, total := 0, 0
	report := func(name string, ok bool) {
		total++
		word := "FAIL"
		if ok {
			word, pass = "OK", pass+1
		}
		fmt.Printf("%s %s\n", word, name)
	}
	a, b := base(10), base(10)
	shuffle.Shuffle(a, 42)
	shuffle.Shuffle(b, 42)
	report("deterministic", fmt.Sprint(a) == fmt.Sprint(b))
	c := base(20)
	shuffle.Shuffle(c, 7)
	report("permutation kept", check.VerifyPermutation(base(20), c) == nil)
	empty, single := []int{}, []int{9}
	shuffle.Shuffle(empty, 1)
	shuffle.Shuffle(single, 1)
	report("empty and single legal", len(empty) == 0 && single[0] == 9)
	report("n=2 balance", check.MaxMinRatio(dist(2, 10000, shuffle.Shuffle[int])) <= 1.2)
	counts := dist(4, 120000, shuffle.Shuffle[int])
	report("uniform n=4", len(counts) == 24 && check.VerifyUniform(counts, 1.5) == nil)
	buggy := dist(7, 600000, func(a []int, seed uint64) { // 事故实现：全范围选
		src := rng.New(seed)
		for i := range a {
			j, _ := src.Intn(len(a))
			a[i], a[j] = a[j], a[i]
		}
	})
	report("buggy full-range skewed", check.MaxMinRatio(buggy) > 5)
	before := shuffle.RandCalls()
	shuffle.Shuffle(base(50), 3)
	report("rand calls n-1", shuffle.RandCalls()-before == 49)
	_, e1 := rng.New(1).Intn(0)
	e2 := check.VerifyPermutation([]int{1}, []int{2})
	e3 := check.VerifyUniform([]int{1, 9}, 1.5)
	sentinels := errors.Is(e1, rng.ErrBadBound) && errors.Is(e2, check.ErrNotPermutation) && errors.Is(e3, check.ErrNonUniform)
	report("sentinel errors", sentinels)
	word := "OK"
	if pass != total {
		word = "FAIL"
	}
	fmt.Printf("%s total %d/%d\n", word, pass, total)
	if pass != total {
		os.Exit(1)
	}
}
