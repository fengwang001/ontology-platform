package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/arr"
	"ontology/check"
	"ontology/cnt"
)

var failed bool

func judge(name string, ok bool) {
	status := "OK "
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Println(status, name)
}

func count(a []int) int64 {
	n, err := cnt.CountInversions(a)
	if err != nil {
		return -1
	}
	return n
}

func crossCheck() bool {
	seed := uint64(7)
	for n := 0; n < 200; n++ {
		a := make([]int, n)
		for i := range a {
			seed = seed*6364136223846793005 + 1442695040888963407
			a[i] = int(seed>>33) % 50
		}
		if count(a) != check.NaiveCount(a) {
			return false
		}
	}
	return true
}

func main() {
	judge("样例 [2,4,1,3,5] = 3", count([]int{2, 4, 1, 3, 5}) == 3)
	judge("完全降序 = n(n-1)/2", count([]int{5, 4, 3, 2, 1}) == 10)
	judge("已升序 = 0", count([]int{1, 2, 3, 4, 5}) == 0)
	judge("与朴素参照一致(n<200)", crossCheck())
	_, errNil := arr.Count(nil)
	judge("nil 触发 ErrNilSlice", errors.Is(errNil, arr.ErrNilSlice))
	_, errNeg := arr.Count([]int{-1})
	judge("负数触发 ErrNegative", errors.Is(errNeg, arr.ErrNegative))
	if failed {
		fmt.Println("FAIL 总计")
		os.Exit(1)
	}
	fmt.Println("OK 总计")
}
