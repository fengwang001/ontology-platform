package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/arr"
	"ontology/check"
	"ontology/kad"
)

var failed bool

func judge(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	mixed := []int{-2, 1, -3, 4, -1, 2, 1, -5, 4}
	judge("kad sum matches naive", kad.MaxSubarraySum(mixed) == check.NaiveMaxSum(mixed))
	judge("all-negative gives -1", kad.MaxSubarraySum([]int{-2, -3, -1}) == -1)
	lo, hi, sum := kad.MaxSubarrayRange(mixed)
	got := 0
	for _, v := range mixed[lo:hi] {
		got += v
	}
	judge("range sum consistent", got == sum && sum == check.NaiveMaxSum(mixed))
	_, errEmpty := arr.MaxSum([]int{})
	judge("empty gives ErrEmpty", errors.Is(errEmpty, arr.ErrEmpty))
	_, errNil := arr.MaxSum(nil)
	judge("nil gives ErrNil", errors.Is(errNil, arr.ErrNil))
	judge("single element", kad.MaxSubarraySum([]int{42}) == 42)
	lo, hi, sum = kad.MaxSubarrayRange([]int{1, 2, 3, 4})
	judge("all-positive whole array", lo == 0 && hi == 4 && sum == 10)
	kad.ResetAccessCount()
	kad.MaxSubarraySum(make([]int, 100000))
	judge("single pass <= n accesses", kad.AccessCount() <= 100000)
	if failed {
		fmt.Println("FAIL total: some checks failed")
		os.Exit(1)
	} else {
		fmt.Println("OK total: all checks passed")
	}
}
