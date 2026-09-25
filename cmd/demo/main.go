package main

import (
	"errors"
	"fmt"

	"ontology/arr"
	"ontology/rot"
)

func main() {
	failures := 0
	nums := []int{4, 5, 6, 7, 0, 1, 2}
	check("rotated hit", rot.Search(nums, 0) == 4, &failures)
	check("rotated miss", rot.Search(nums, 3) == -1, &failures)
	check("ordered search", rot.Search([]int{1, 2, 3}, 2) == 1, &failures)

	emptyIndex, emptyErr := arr.Search([]int{}, 1)
	check("empty returns miss", emptyIndex == -1 && emptyErr == nil, &failures)
	_, nilErr := arr.Search(nil, 1)
	check("nil sentinel", errors.Is(nilErr, arr.ErrNilInput), &failures)
	_, duplicateErr := arr.Search([]int{2, 2, 3}, 1)
	check("duplicate sentinel", errors.Is(duplicateErr, arr.ErrDuplicate), &failures)
	_, invalidErr := arr.Search([]int{3, 1, 2, 0}, 1)
	check("invalid sentinel", errors.Is(invalidErr, arr.ErrInvalidRotate), &failures)

	status := "OK"
	if failures != 0 {
		status = "FAIL"
	}
	fmt.Printf("%s total: %d failure(s)\n", status, failures)
	if failures != 0 {
		panic("demo checks failed")
	}
}

func check(name string, ok bool, failures *int) {
	status := "OK"
	if !ok {
		status = "FAIL"
		*failures++
	}
	fmt.Printf("%s %s\n", status, name)
}
