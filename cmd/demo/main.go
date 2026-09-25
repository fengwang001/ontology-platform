package main

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"ontology/check"
	"ontology/rng"
	"ontology/shuffle"
)

var failed bool

func judge(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func permCounts(n, trials int, shuf func([]int, uint64)) []int64 {
	counts := map[string]int64{}
	for t := 0; t < trials; t++ {
		a := make([]int, n)
		for i := range a {
			a[i] = i
		}
		shuf(a, uint64(t))
		counts[fmt.Sprint(a)]++
	}
	out := []int64{}
	for _, c := range counts {
		out = append(out, c)
	}
	return out
}

func fair(a []int, s uint64) { _ = shuffle.Shuffle(a, s) }

func naive(a []int, s uint64) {
	g := rng.New(s)
	for i := 0; i < len(a)-1; i++ {
		j, _ := g.Intn(len(a))
		a[i], a[j] = a[j], a[i]
	}
}

func main() {
	a, b := []int{0, 1, 2, 3, 4}, []int{0, 1, 2, 3, 4}
	_ = shuffle.Shuffle(a, 42)
	_ = shuffle.Shuffle(b, 42)
	judge("deterministic", slices.Equal(a, b))
	slices.Sort(b)
	judge("permutation kept", slices.Equal(b, []int{0, 1, 2, 3, 4}))
	errEmpty := shuffle.Shuffle([]int{}, 1)
	errSingle := shuffle.Shuffle([]int{7}, 1)
	judge("empty/single legal", errEmpty == nil && errSingle == nil)
	c := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	_ = shuffle.Shuffle(c, 7)
	judge("rand calls n-1", shuffle.RandCalls() == 9)
	u4 := permCounts(4, 120000, fair)
	r4, _ := check.MaxMinRatio(u4)
	chi4, _ := check.ChiSquare(u4)
	judge("uniform n=4", len(u4) == 24 && r4 <= 1.5 && chi4 <= 72)
	n5 := permCounts(5, 120000, naive)
	r5, _ := check.MaxMinRatio(n5)
	judge("naive skewed", len(n5) == 120 && r5 > 5)
	u2 := permCounts(2, 10000, fair)
	r2, _ := check.MaxMinRatio(u2)
	judge("n=2 balance", len(u2) == 2 && r2 <= 1.5)
	_, e1 := rng.New(1).Intn(0)
	e2 := shuffle.Shuffle[int](nil, 1)
	_, e3 := check.MaxMinRatio(nil)
	ok := errors.Is(e1, rng.ErrNonPositiveN) && errors.Is(e2, shuffle.ErrNilSlice) && errors.Is(e3, check.ErrEmptyCounts)
	judge("sentinel errors", ok)
	if failed {
		fmt.Println("FAIL total")
		os.Exit(1)
	}
	fmt.Println("OK total")
}
