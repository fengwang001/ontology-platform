// 区间覆盖计数器演示。运行：go run ./cmd/demo
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"

	"ontology/coverage"
)

func main() {
	fails := 0
	check := func(name string, ok bool, detail string) {
		tag := "OK"
		if !ok {
			tag = "FAIL"
			fails++
		}
		fmt.Printf("%s %s %s\n", tag, name, detail)
	}

	// 1) 同一区间加三次 -> 三层覆盖。
	c := coverage.New()
	for i := 0; i < 3; i++ {
		_ = c.Add(0, 10)
	}
	check("add-three-times", c.CountAt(5).Count == 3,
		fmt.Sprintf("CountAt(5)=%d", c.CountAt(5).Count))

	// 2) Remove 一次减一层。
	_ = c.Remove(0, 10)
	check("remove-one-layer", c.CountAt(5).Count == 2,
		fmt.Sprintf("CountAt(5)=%d", c.CountAt(5).Count))

	// 3) 多减一次 -> 可判定错误，计数不变负。
	err := c.Remove(100, 200)
	check("over-remove-error", errors.Is(err, coverage.ErrNotTracked),
		fmt.Sprintf("err=%v", err))

	// 4) CountAt(lo) 命中、CountAt(hi) 不命中。
	c2 := coverage.New()
	_ = c2.Add(3, 7)
	atLo, atHi := c2.CountAt(3).Count, c2.CountAt(7).Count
	check("endpoint-semantics", atLo == 1 && atHi == 0,
		fmt.Sprintf("CountAt(3)=%d CountAt(7)=%d", atLo, atHi))

	// 5) 三个重叠区间的规范分段。
	c3 := coverage.New()
	for _, iv := range [][2]int64{{0, 10}, {5, 15}, {10, 20}} {
		_ = c3.Add(iv[0], iv[1])
	}
	segs := c3.Segments()
	want := []coverage.Segment{
		{Lo: 0, Hi: 5, Count: 1},
		{Lo: 5, Hi: 15, Count: 2},
		{Lo: 15, Hi: 20, Count: 1},
	}
	merged := len(segs) == len(want)
	for i := range want {
		merged = merged && segs[i] == want[i]
	}
	check("segments-three-overlap", merged, fmt.Sprintf("%v", segs))

	// 6) 打乱添加顺序 -> Segments 逐元素一致。
	c3b := coverage.New()
	for _, k := range []int{2, 0, 1} {
		iv := [][2]int64{{0, 10}, {5, 15}, {10, 20}}[k]
		_ = c3b.Add(iv[0], iv[1])
	}
	segsB := c3b.Segments()
	sameOrder := len(segsB) == len(segs)
	for i := range segs {
		sameOrder = sameOrder && segsB[i] == segs[i]
	}
	check("segments-order-independent", sameOrder, fmt.Sprintf("%v", segsB))

	// 7) 空区间错误。
	err = c.Add(5, 5)
	check("empty-interval-error", errors.Is(err, coverage.ErrEmptyInterval),
		fmt.Sprintf("err=%v", err))

	// 8) 极值区间不溢出。
	c4 := coverage.New()
	extErr := c4.Add(math.MinInt64, math.MaxInt64)
	extOK := extErr == nil &&
		c4.CountAt(math.MinInt64).Count == 1 &&
		c4.CountAt(math.MaxInt64).Count == 0 &&
		c4.Verify() == nil
	check("extreme-endpoints", extOK, "[MinInt64,MaxInt64) added, counts correct")

	// 9) 上万区间：单点查询检查数为对数量级。
	const n = 20000
	big := coverage.New()
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < n; i++ {
		lo := rng.Int63n(1 << 40)
		_ = big.Add(lo, lo+1+rng.Int63n(1000))
	}
	worst := 0
	for i := 0; i < 50; i++ {
		r := big.CountAt(rng.Int63n(1 << 40))
		if r.EndpointsExamined > worst {
			worst = r.EndpointsExamined
		}
	}
	check("logarithmic-probes", worst <= 20,
		fmt.Sprintf("intervals=%d worst-endpoints-examined=%d", n, worst))

	// 10) MaxCoverage 层数与位置。
	c5 := coverage.New()
	_ = c5.Add(0, 100)
	_ = c5.Add(40, 100)
	_ = c5.Add(40, 60)
	m := c5.MaxCoverage()
	check("max-coverage", m.Found && m.Count == 3 && m.Start == 40,
		fmt.Sprintf("max=%d at %d", m.Count, m.Start))

	if fails == 0 {
		fmt.Printf("TOTAL: 10/10 checks OK\n")
	} else {
		fmt.Printf("TOTAL: %d check(s) FAILed\n", fails)
	}
}
