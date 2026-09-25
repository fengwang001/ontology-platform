package main

import (
	"errors"
	"fmt"
	"slices"

	"ontology/check"
	"ontology/shuffle"
)

var failed bool

func report(ok bool, name string) {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		failed = true
		fmt.Println("FAIL " + name)
	}
}

func main() {
	a, b := []int{0, 1, 2, 3}, []int{0, 1, 2, 3}
	_ = shuffle.Shuffle(a, 42)
	_ = shuffle.Shuffle(b, 42)
	report(slices.Equal(a, b), "deterministic: same seed same result")
	sorted := slices.Clone(a)
	slices.Sort(sorted)
	report(slices.Equal(sorted, []int{0, 1, 2, 3}), "permutation preserved")
	report(shuffle.Shuffle([]int{}, 1) == nil && shuffle.Shuffle([]int{7}, 1) == nil, "empty/single ok")
	report(errors.Is(shuffle.Shuffle([]int(nil), 1), shuffle.ErrNilSlice), "nil -> ErrNilSlice")
	before := shuffle.RandCalls()
	_ = shuffle.Shuffle(make([]int, 10), 1)
	report(shuffle.RandCalls()-before == 9, "rand calls == n-1")
	c2, _ := check.CountPermutations(2, 20000, 1)
	report(check.MaxMinRatio(c2) < 1.2, "n=2 roughly 50/50")
	report(check.VerifyUniform(4, 120000, 1, 1.5) == nil, "uniform n=4 max/min<=1.5")
	if failed {
		fmt.Println("FAIL total: some checks failed")
	} else {
		fmt.Println("OK   total: all checks passed")
	}
}
