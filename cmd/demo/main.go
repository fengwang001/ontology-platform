// Command demo 演示 segtree 的区间加与区间求和。
package main

import (
	"errors"
	"fmt"
	"math"
	"os"

	"ontology/check"
	"ontology/seg"
	"ontology/segtree"
)

var fails int

func judge(name string, ok bool) {
	if ok {
		fmt.Println("OK", name)
		return
	}
	fmt.Println("FAIL", name)
	fails++
}

func main() {
	const n = 1000
	tr, ref := segtree.New(n), check.NewNaive(n)
	judge("add-full", tr.AddRange(0, n-1, 1) == nil)
	ref.AddRange(0, n-1, 1)
	v, err := tr.SumRange(0, 0)
	judge("query-pushdown", err == nil && v == 1) // 查询下推：单点读到根的 delta
	judge("add-range", tr.AddRange(3, 9, 5) == nil)
	ref.AddRange(3, 9, 5)
	s, err := tr.SumRange(0, n-1)
	judge("mixed-sum", err == nil && s == ref.SumRange(0, n-1))
	p, err := tr.SumRange(5, 5)
	judge("single-point", err == nil && p == ref.SumRange(5, 5))
	_, err = tr.SumRange(5, 2)
	judge("reversed-err", errors.Is(err, seg.ErrBadRange))
	_, err = tr.SumRange(0, n)
	judge("oob-err", errors.Is(err, seg.ErrBadRange))
	judge("visited-bound", tr.LastVisited() <= int(4*math.Log2(n))+8)
	if fails > 0 {
		fmt.Println("FAIL total:", fails, "failed")
		os.Exit(1)
	}
	fmt.Println("OK total: all passed")
}
