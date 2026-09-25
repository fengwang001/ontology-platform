package main

import (
	"errors"
	"fmt"
	"math/bits"

	"ontology/arr"
	"ontology/rot"
)

func report(tag string, ok bool, passed, total *int) {
	*total++
	if ok {
		*passed++
		fmt.Printf("OK %s\n", tag)
	} else {
		fmt.Printf("FAIL %s\n", tag)
	}
}

func main() {
	nums := []int{4, 5, 6, 7, 0, 1, 2}
	passed, total := 0, 0
	report("hit 0 -> 4", rot.Search(nums, 0) == 4, &passed, &total)
	report("miss 3 -> -1", rot.Search(nums, 3) == -1, &passed, &total)
	report("single element", rot.Search([]int{9}, 9) == 0 && rot.Search([]int{9}, 0) == -1, &passed, &total)
	report("no rotation", rot.Search([]int{1, 2, 3, 4, 5}, 4) == 3, &passed, &total)
	report("empty -> -1", rot.Search([]int{}, 1) == -1, &passed, &total)
	const n = 100000
	big := make([]int, n)
	for i := range big {
		big[i] = ((i - 12345) + n) % n
	}
	_, cmp := rot.SearchWithCount(big, 77)
	bound := 2*bits.Len(uint(n-1)) + 4
	report("comparisons within bound", cmp <= bound, &passed, &total)
	_, err := arr.Search(nil, 1)
	report("nil gives ErrNilInput", errors.Is(err, arr.ErrNilInput), &passed, &total)
	if passed == total {
		fmt.Printf("OK TOTAL: %d/%d\n", passed, total)
	} else {
		fmt.Printf("FAIL TOTAL: %d/%d\n", passed, total)
	}
}
