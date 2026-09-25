// Command demo exercises sel.KthSmallest with a few fixed checks.
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/check"
	"ontology/ord"
	"ontology/sel"
)

func main() {
	pass, total := 0, 0
	report := func(ok bool, msg string) {
		total++
		if ok {
			pass++
			fmt.Println("OK", msg)
		} else {
			fmt.Println("FAIL", msg)
		}
	}
	got, _ := sel.KthSmallest([]int{3, 2, 1, 5, 4}, 2)
	report(got == 3, "kth of [3 2 1 5 4] k=2 is 3")
	got, _ = sel.KthSmallest([]int{3, 2, 3, 1, 2}, 3)
	report(got == 3, "duplicates [3 2 3 1 2] k=3 is 3")
	got, _ = sel.KthSmallest([]int{9, 4, 7, 1}, 0)
	report(got == 1, "k=0 returns min")
	got, _ = sel.KthSmallest([]int{9, 4, 7, 1}, 3)
	report(got == 9, "k=n-1 returns max")
	_, err := sel.KthSmallest([]int{1}, 5)
	report(errors.Is(err, ord.ErrBadK), "k out of range is ErrBadK")
	_, err = sel.KthSmallest([]int(nil), 0)
	report(errors.Is(err, ord.ErrEmpty), "empty slice is ErrEmpty")
	got, _ = sel.KthSmallest([]int{5, 1, 4, 2, 3}, 2)
	report(got == check.KthRef([]int{5, 1, 4, 2, 3}, 2), "matches sort reference")
	fmt.Printf("OK %d/%d checks passed\n", pass, total)
	if pass != total {
		os.Exit(1)
	}
}
