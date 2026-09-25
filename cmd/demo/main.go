// demo 依次验证 part / ord / check 三个包的关键语义，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/check"
	"ontology/ord"
	"ontology/part"
)

var passed, total int

func judge(name string, ok bool) {
	total++
	if ok {
		passed++
		fmt.Printf("OK %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
	}
}

func main() {
	in := []int{2, 0, 2, 1, 1, 0}
	arr := slices.Clone(in)
	lt, gt := part.ThreeWayPartition(arr, 1)
	judge("part: pinned 用例精确重排", slices.Equal(arr, []int{0, 0, 1, 1, 2, 2}))

	wlt, wgt := check.Naive(slices.Clone(in), 1)
	judge("part: 区间与朴素参照一致", lt == wlt && gt == wgt)

	judge("ord: 三段不变量校验通过", ord.Verify(arr, 1, lt, gt) == nil)

	const n = 10000
	before := part.Swaps()
	part.ThreeWayPartition(make([]int, n), 0)
	judge("part: 全相等输入交换次数<=n", part.Swaps()-before <= n)

	err := ord.Verify([]int{5, 1, 9}, 1, 1, 2)
	judge("ord: 哨兵错误可用 errors.Is 区分", errors.Is(err, ord.ErrLowerSegment) && !errors.Is(err, ord.ErrEqualSegment))

	_, _, perr := ord.Partition(slices.Clone(in), 1)
	judge("ord: Partition 合法输入无错", perr == nil)

	var wg sync.WaitGroup
	var bad atomic.Bool
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := []int{3, 1, 2, 1, 0, 2, 1}
			if l, g := part.ThreeWayPartition(a, 1); ord.Verify(a, 1, l, g) != nil {
				bad.Store(true)
			}
		}()
	}
	wg.Wait()
	judge("part: 并发调用互不影响", !bad.Load())

	status := "OK"
	if passed != total {
		status = "FAIL"
	}
	fmt.Printf("%s total: %d/%d checks passed\n", status, passed, total)
	if passed != total {
		os.Exit(1)
	}
}
