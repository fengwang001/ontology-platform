package main

import (
	"errors"
	"fmt"
	"math"

	"ontology/arr"
	"ontology/check"
	"ontology/cnt"
)

func report(name string, ok bool, failures *int) {
	if ok {
		fmt.Printf("OK %s\n", name)
	} else {
		*failures++
		fmt.Printf("FAIL %s\n", name)
	}
}

func main() {
	failures := 0

	pinned, _ := cnt.CountInversions([]int{2, 4, 1, 3, 5})
	report("pinned example gives 3", pinned == 3, &failures)

	empty, _ := cnt.CountInversions(nil)
	single, _ := cnt.CountInversions([]int{42})
	report("nil and single give 0", empty == 0 && single == 0, &failures)

	sorted, _ := cnt.CountInversions([]int{1, 2, 3, 4, 5})
	desc, _ := cnt.CountInversions([]int{5, 4, 3, 2, 1})
	report("ascending 0 and descending n(n-1)/2", sorted == 0 && desc == 10, &failures)

	ties, _ := cnt.CountInversions([]int{3, 3, 3, 1, 1})
	report("equal elements are stable (ties not counted)", ties == 6, &failures)

	report("naive O(n^2) reference agrees",
		check.Agrees([]int{7, 2, 9, 2, 1, 6, 3}), &failures)

	_, nilErr := arr.Count(nil)
	_, negErr := arr.Count([]int{1, -1})
	report("sentinel errors distinguishable",
		errors.Is(nilErr, arr.ErrNilSlice) && errors.Is(negErr, arr.ErrNegativeValue),
		&failures)

	desc100k := make([]int, 100_000)
	for i := range desc100k {
		desc100k[i] = 100_000 - i
	}
	big, comparisons, _ := cnt.CountInversionsWithStats(desc100k)
	wantBig := int64(100_000) * 99_999 / 2
	bound := 100_000*math.Log2(100_000) + 100_000
	report("n=100000 count exact and comparisons bounded",
		big == wantBig && float64(comparisons) <= bound, &failures)

	concurrentOK := true
	done := make(chan bool, 8)
	for g := 0; g < 8; g++ {
		go func() {
			got, _ := cnt.CountInversions([]int{2, 4, 1, 3, 5})
			done <- got == 3
		}()
	}
	for g := 0; g < 8; g++ {
		if !<-done {
			concurrentOK = false
		}
	}
	report("concurrent calls are independent", concurrentOK, &failures)

	fmt.Printf("OK total: %d/%d\n", 8-failures, 8)
	if failures > 0 {
		fmt.Println("FAIL some checks failed")
	}
}
