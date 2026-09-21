// 区间覆盖计数器演示：逐项演练并打印 OK/FAIL 判定，最后打印总计。
// 不读命令行参数、不联网，退出码恒为 0。
package main

import (
	"errors"
	"fmt"
	"math"
	"slices"

	"ontology"
)

var passed, total int

func check(name string, ok bool, detail string) {
	total++
	verdict := "FAIL"
	if ok {
		verdict = "OK"
		passed++
	}
	fmt.Printf("%s %s: %s\n", verdict, name, detail)
}

func main() {
	// 1. 同一区间加三次 -> 三层覆盖。
	c := ontology.New()
	for i := 0; i < 3; i++ {
		_ = c.Add(10, 20)
	}
	n, _ := c.CountAt(15)
	check("triple-add", n == 3, fmt.Sprintf("CountAt(15)=%d after 3 adds of [10,20)", n))

	// 2. Remove 一次 -> 减一层。
	_ = c.Remove(10, 20)
	n, _ = c.CountAt(15)
	check("remove-one-layer", n == 2, fmt.Sprintf("CountAt(15)=%d after 1 remove", n))

	// 3. 多减一次 -> 可判定错误。
	_ = c.Remove(10, 20)
	_ = c.Remove(10, 20)
	err := c.Remove(10, 20)
	check("over-remove-error", errors.Is(err, ontology.ErrNotPresent),
		fmt.Sprintf("err=%v", err))

	// 4. CountAt(lo) 命中，CountAt(hi) 不命中。
	c2 := ontology.New()
	_ = c2.Add(10, 20)
	lo, _ := c2.CountAt(10)
	hi, _ := c2.CountAt(20)
	check("endpoint-semantics", lo == 1 && hi == 0,
		fmt.Sprintf("CountAt(10)=%d CountAt(20)=%d", lo, hi))

	// 5. 三个重叠区间的规范分段。
	c3 := ontology.New()
	_ = c3.Add(0, 10)
	_ = c3.Add(5, 15)
	_ = c3.Add(10, 20)
	segs := c3.Segments()
	want := []ontology.Segment{
		{Lo: 0, Hi: 5, Count: 1},
		{Lo: 5, Hi: 15, Count: 2},
		{Lo: 15, Hi: 20, Count: 1},
	}
	check("segments-3-overlap", slices.Equal(segs, want), fmt.Sprintf("%v", segs))

	// 6. 打乱添加顺序 -> Segments 逐元素一致。
	c4 := ontology.New()
	_ = c4.Add(10, 20)
	_ = c4.Add(0, 10)
	_ = c4.Add(5, 15)
	check("segments-order-free", slices.Equal(c4.Segments(), want),
		fmt.Sprintf("%v", c4.Segments()))

	// 7. 空区间 -> 可判定错误。
	err = c4.Add(5, 5)
	check("empty-interval-error", errors.Is(err, ontology.ErrEmptyInterval),
		fmt.Sprintf("err=%v", err))

	// 8. 极值端点 [MinInt64, MaxInt64) 正常添加。
	c5 := ontology.New()
	err = c5.Add(math.MinInt64, math.MaxInt64)
	mid, _ := c5.CountAt(0)
	check("extreme-endpoints", err == nil && mid == 1,
		fmt.Sprintf("CountAt(0)=%d err=%v", mid, err))

	// 9. 上万区间下单点查询的检查数（对数量级）。
	c6 := ontology.New()
	for i := 0; i < 20000; i++ {
		_ = c6.Add(int64(i), int64(i+1000))
	}
	got, checked := c6.CountAt(15000)
	check("log-query-checks", got == 1000 && checked <= 32,
		fmt.Sprintf("20000 intervals, CountAt=%d, checked=%d endpoints", got, checked))

	// 10. MaxCoverage 的层数与位置。
	mc, start, ok := c6.MaxCoverage()
	atStart, _ := c6.CountAt(start)
	check("max-coverage", ok && mc == 1000 && atStart == mc,
		fmt.Sprintf("max=%d at %d", mc, start))

	fmt.Printf("TOTAL %d/%d OK\n", passed, total)
}
