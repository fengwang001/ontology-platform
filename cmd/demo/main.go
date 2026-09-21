package main

import (
	"errors"
	"fmt"
	"math"
	"os"

	"ontology/internal/alloc"
)

func main() {
	failed := false
	check := func(name string, ok bool) {
		status := "OK"
		if !ok {
			status = "FAIL"
			failed = true
		}
		fmt.Printf("[%s] %s\n", status, name)
	}
	sum := func(xs []int64) int64 {
		var s int64
		for _, x := range xs {
			s += x
		}
		return s
	}

	r, err := alloc.Allocate(100, []int64{1, 2, 1})
	check("基本分摊 100 -> [25 50 25]", err == nil && equal(r, []int64{25, 50, 25}))

	r, err = alloc.Allocate(10, []int64{1, 1, 1})
	check("最大余数法 [4 3 3]", err == nil && equal(r, []int64{4, 3, 3}))

	r, err = alloc.Allocate(-7, []int64{1, 1, 1})
	check("负金额 floor [-2 -2 -3]", err == nil && equal(r, []int64{-2, -2, -3}))

	r, err = alloc.Allocate(10, []int64{3, 2, 1})
	check("余数排序 [5 3 2]", err == nil && equal(r, []int64{5, 3, 2}))

	r, err = alloc.Allocate(10, []int64{0, 1, 0, 2})
	check("零权重零分配", err == nil && r[0] == 0 && r[2] == 0 && sum(r) == 10)

	r, err = alloc.Allocate(100, []int64{3, 1, 4, 1, 5})
	check("守恒 sum==amount", err == nil && sum(r) == 100)

	r2, err2 := alloc.Allocate(100, []int64{3, 1, 4, 1, 5})
	check("确定性：两次调用一致", err == nil && err2 == nil && equal(r, r2))

	r, err = alloc.Allocate(-12345, []int64{7, 2, 9})
	check("负金额守恒", err == nil && sum(r) == -12345)

	r, err = alloc.Split(10, 3)
	check("Split 10/3 -> [4 3 3]", err == nil && equal(r, []int64{4, 3, 3}))

	r, err = alloc.Split(-10, 3)
	check("Split -10/3 -> [-3 -3 -4]", err == nil && equal(r, []int64{-3, -3, -4}))

	_, err = alloc.Allocate(1, nil)
	check("空权重报错 ErrNoWeights", errors.Is(err, alloc.ErrNoWeights))

	_, err = alloc.Allocate(1, []int64{1, -1})
	check("负权重报错 ErrNegativeWeight", errors.Is(err, alloc.ErrNegativeWeight))

	_, err = alloc.Allocate(1, []int64{0, 0})
	check("零和权重报错 ErrZeroTotalWeight", errors.Is(err, alloc.ErrZeroTotalWeight))

	_, err = alloc.Allocate(math.MaxInt64, []int64{2})
	check("溢出报错 ErrOverflow", errors.Is(err, alloc.ErrOverflow))

	_, err = alloc.Split(1, 0)
	check("Split n<=0 报错 ErrInvalidParts", errors.Is(err, alloc.ErrInvalidParts))

	if failed {
		os.Exit(1)
	}
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
