package main

import (
	"errors"
	"fmt"

	"ontology/arr"
	"ontology/check"
	"ontology/kad"
)

var failed bool

func report(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	mixed := []int{-2, 1, -3, 4, -1, 2, 1, -5, 4}
	report("all-negative returns max negative", kad.MaxSubarraySum([]int{-2, -3, -1}) == -1)
	report("kad matches naive reference", kad.MaxSubarraySum(mixed) == check.NaiveMaxSum(mixed))
	lo, hi, sum := kad.MaxSubarrayRange(mixed)
	got := 0
	for _, v := range mixed[lo : hi+1] {
		got += v
	}
	report("range is self-consistent", got == sum && sum == 6)
	report("single element returned as-is", kad.MaxSubarraySum([]int{7}) == 7)
	report("all-positive sums whole array", kad.MaxSubarraySum([]int{1, 2, 3, 4}) == 10)
	_, err := arr.MaxSum([]int{})
	report("empty input yields ErrEmpty", errors.Is(err, arr.ErrEmpty))
	_, err = arr.MaxSum(nil)
	report("nil input yields ErrNil", errors.Is(err, arr.ErrNil))

	if failed {
		fmt.Println("FAIL total: some checks failed")
	} else {
		fmt.Println("OK total: all checks passed")
	}
}
