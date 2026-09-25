// Command demo exercises the three-way partition packages end to end.
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"ontology/check"
	"ontology/ord"
	"ontology/part"
)

func main() {
	pass, total := 0, 0
	report := func(ok bool, name string) {
		total++
		if ok {
			pass++
			fmt.Println("OK", name)
		} else {
			fmt.Println("FAIL", name)
		}
	}

	arr := []int{2, 0, 2, 1, 1, 0}
	lt, gt := part.ThreeWayPartition(arr, 1)
	report(slices.Equal(arr, []int{0, 0, 1, 1, 2, 2}) && lt == 2 && gt == 4, "pinned-order")

	lt, gt = part.ThreeWayPartition([]int{}, 1)
	report(lt == 0 && gt == 0, "empty")

	lt, gt = part.ThreeWayPartition([]int{7}, 7)
	report(lt == 0 && gt == 1, "single-equal")

	const n = 10000
	part.ResetSwaps()
	part.ThreeWayPartition(make([]int, n), 0)
	report(part.Swaps() <= n, "swap-bound-all-equal")

	_, _, err := ord.Checked([]int{3, 1, 4, 1, 5, 9, 2, 6}, 4)
	report(err == nil, "checked-mixed")

	report(check.Consistent([]int{5, 3, 4, 2}, 1), "consistent-all-greater")

	err = ord.Verify([]int{1}, 1, 2, 3)
	report(errors.Is(err, ord.ErrBadRange), "sentinel-error")

	word := "OK"
	if pass != total {
		word = "FAIL"
	}
	fmt.Printf("%s total %d/%d\n", word, pass, total)
	if pass != total {
		os.Exit(1)
	}
}
