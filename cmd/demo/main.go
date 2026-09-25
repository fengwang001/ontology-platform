package main

import (
	"errors"
	"fmt"

	"ontology/arr"
	"ontology/check"
	"ontology/rot"
)

func main() {
	checks := []struct {
		name string
		ok   bool
	}{
		{"rot hit", rot.Search([]int{4, 5, 6, 7, 0, 1, 2}, 0) == 4},
		{"rot miss", rot.Search([]int{4, 5, 6, 7, 0, 1, 2}, 3) == -1},
		{"rot linear", check.LinearSearch([]int{1, 2}, 2) == 1},
		{"arr empty", func() bool { index, err := arr.Search(nil, 1); return index == -1 && err == nil }()},
		{"arr duplicate", errors.Is(arr.Validate([]int{1, 1}), arr.ErrDuplicate)},
		{"arr invalid", errors.Is(arr.Validate([]int{3, 1, 0}), arr.ErrNotRotatedSorted)},
	}

	failed := 0
	for _, item := range checks {
		if item.ok {
			fmt.Printf("OK %s\n", item.name)
		} else {
			failed++
			fmt.Printf("FAIL %s\n", item.name)
		}
	}
	fmt.Printf("OK total: %d passed, %d failed\n", len(checks)-failed, failed)
	if failed > 0 {
		fmt.Print("FAIL demo\n")
	}
}
