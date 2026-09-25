// Command demo exercises the quickselect packages with a few checks.
package main

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"

	"ontology/check"
	"ontology/ord"
	"ontology/sel"
)

var passed, total int

func judge(name string, ok bool) {
	total++
	if ok {
		passed++
		fmt.Println("OK", name)
	} else {
		fmt.Println("FAIL", name)
	}
}

func main() {
	got1, _ := sel.KthSmallest([]int{3, 2, 1, 5, 4}, 2)
	judge("pinned k=2 -> 3", got1 == 3)
	got2, _ := sel.KthSmallest([]int{3, 2, 3, 1, 2}, 3)
	judge("pinned dups k=3 -> 3", got2 == 3)
	lo, _ := sel.KthSmallest([]int{9, 4, 7, 1}, 0)
	judge("k=0 -> min", lo == 1)
	hi, _ := sel.KthSmallest([]int{9, 4, 7, 1}, 3)
	judge("k=n-1 -> max", hi == 9)
	_, errEmpty := sel.KthSmallest([]int{}, 0)
	judge("empty -> ErrEmpty", errors.Is(errEmpty, ord.ErrEmpty))
	_, errK := sel.KthSmallest([]int{1}, 5)
	judge("bad k -> ErrBadK", errors.Is(errK, ord.ErrBadK))
	arr := rand.Perm(4096)
	ref, _ := check.KthSmallestRef(slices.Clone(arr), 2048)
	fast, _ := sel.KthSmallest(arr, 2048)
	judge("matches sort reference", fast == ref)
	sel.ResetComparisons()
	for range 5 {
		_, _ = sel.KthSmallest(rand.Perm(100000), 50000)
	}
	judge("avg comparisons <= 4n", sel.Comparisons()/5 <= 4*100000)
	fmt.Printf("%s total %d/%d\n", map[bool]string{true: "OK", false: "FAIL"}[passed == total], passed, total)
}
