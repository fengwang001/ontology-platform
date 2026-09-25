package main

import (
	"errors"
	"fmt"

	"ontology/arr"
	"ontology/rot"
)

func main() {
	nums := []int{4, 5, 6, 7, 0, 1, 2}
	passed, failed := 0, 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Printf("OK %s\n", name)
			passed++
		} else {
			fmt.Printf("FAIL %s\n", name)
			failed++
		}
	}

	check("skeleton runs", true)
	check("hit index", rot.Search(nums, 0) == 4)
	check("miss target", rot.Search(nums, 3) == -1)
	check("single hit", rot.Search([]int{9}, 9) == 0)
	check("empty gives -1", rot.Search(nil, 1) == -1)
	check("no rotation", rot.Search([]int{1, 2, 3, 4}, 4) == 3)
	_, err := arr.Search(nil, 1)
	check("sentinel ErrEmpty", errors.Is(err, arr.ErrEmpty))
	_, err = arr.Search([]int{1, 3, 2}, 2)
	check("sentinel ErrNotRotated", errors.Is(err, arr.ErrNotRotated))

	fmt.Printf("total: %d passed, %d failed\n", passed, failed)
}
