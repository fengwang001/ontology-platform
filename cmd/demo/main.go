package main

import (
	"errors"
	"fmt"

	"ontology/check"
	"ontology/ord"
	"ontology/sel"
)

func report(name string, ok bool) bool {
	if ok {
		fmt.Println("OK", name)
	} else {
		fmt.Println("FAIL", name)
	}
	return ok
}

func main() {
	checks := []bool{
		report("kth", func() bool { v, err := sel.KthSmallest([]int{3, 2, 1, 5, 4}, 2); return err == nil && v == 3 }()),
		report("duplicates", func() bool { v, err := sel.KthSmallest([]int{3, 2, 3, 1, 2}, 3); return err == nil && v == 3 }()),
		report("errors", func() bool {
			_, empty := sel.KthSmallest([]int(nil), 0)
			_, bad := sel.KthSmallest([]int{1}, 1)
			return errors.Is(empty, ord.ErrEmpty) && errors.Is(bad, ord.ErrBadK)
		}()),
		report("reference", func() bool {
			got, err := sel.KthSmallest([]int{7, 1, 4, 2, 9}, 2)
			want, refErr := check.KthSmallestSorted([]int{7, 1, 4, 2, 9}, 2)
			return err == nil && refErr == nil && got == want
		}()),
	}
	pass := 0
	for _, ok := range checks {
		if ok {
			pass++
		}
	}
	fmt.Printf("TOTAL %d/%d OK\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo failed")
	} else {
		return
	}
}
