// Command demo 验证 Fisher-Yates 洗牌的核心语义，逐行打印 OK/FAIL。
package main

import (
	"errors"
	"fmt"
	"slices"

	"ontology/check"
	"ontology/rng"
	"ontology/shuffle"
)

func main() {
	passed, total := 0, 0
	report := func(ok bool, name string) {
		total++
		if ok {
			passed++
			fmt.Println("OK", name)
		} else {
			fmt.Println("FAIL", name)
		}
	}

	a, b := []int{1, 2, 3, 4, 5}, []int{1, 2, 3, 4, 5}
	shuffle.Shuffle(a, 42)
	shuffle.Shuffle(b, 42)
	report(slices.Equal(a, b), "同 seed 结果可复现")
	report(check.IsPermutation([]int{1, 2, 3, 4, 5}, a), "洗牌后是多集置换")

	empty, single := []int{}, []int{9}
	shuffle.Shuffle(empty, 1)
	shuffle.Shuffle(single, 1)
	report(len(empty) == 0 && slices.Equal(single, []int{9}), "空/单元素原样返回")

	shuffle.ResetRandomCalls()
	shuffle.Shuffle(make([]int, 10), 1)
	report(shuffle.RandomCalls() == 9, "n=10 恰好调用 9 次随机")

	c2, _ := check.Distribution(2, 100000, shuffle.Shuffle[int])
	ok2 := len(c2) == 2
	for _, c := range c2 {
		p := float64(c) / 100000
		ok2 = ok2 && p > 0.45 && p < 0.55
	}
	report(ok2, "n=2 两种排列各约 50%")

	c4, _ := check.Distribution(4, 120000, shuffle.Shuffle[int])
	ratio, _ := check.MaxMinRatio(c4)
	chi, _ := check.ChiSquare(c4)
	report(len(c4) == 24 && ratio <= 1.5 && chi < 60, "n=4 均匀(24种,比<=1.5,卡方<60)")

	_, errBound := rng.New(1).Intn(0)
	_, errEmpty := check.MaxMinRatio(nil)
	report(errors.Is(errBound, rng.ErrBadBound) && errors.Is(errEmpty, check.ErrNoCounts),
		"哨兵错误可用 errors.Is 区分")

	if passed == total {
		fmt.Printf("OK total %d/%d passed\n", passed, total)
		return
	}
	fmt.Printf("FAIL total %d/%d passed\n", passed, total)
	panic("demo checks failed")
}
