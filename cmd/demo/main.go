// demo 逐项演示凸多边形直径（旋转卡壳）的正确性，全部通过退出码为 0。
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/cal"
	"ontology/rc"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Printf("%s OK\n", name)
	} else {
		fmt.Printf("%s FAIL\n", name)
		failed = true
	}
}

func regularish(m int, r float64) []cal.Point {
	p := make([]cal.Point, m)
	for i := range p {
		t := 2 * math.Pi * float64(i) / float64(m)
		p[i] = cal.Point{X: int64(math.Round(r * math.Cos(t))), Y: int64(math.Round(r * math.Sin(t)))}
	}
	return p
}

func brute(poly []cal.Point) int64 {
	best := int64(-1)
	for i := range poly {
		for j := i + 1; j < len(poly); j++ {
			best = max(best, cal.Dist2(poly[i], poly[j]))
		}
	}
	return best
}

func main() {
	pent := []cal.Point{{X: 0, Y: 0}, {X: 5, Y: 1}, {X: 6, Y: 4}, {X: 3, Y: 6}, {X: 1, Y: 5}}

	// cal：五条边的对跖顶点与两个 d²，与 NOTES.md 手算表一致
	want := [][3]int64{{3, 45, 29}, {4, 32, 26}, {0, 52, 45}, {1, 29, 32}, {2, 26, 52}}
	ok := true
	for i, w := range want {
		j := cal.Antipodal(pent, i)
		if int64(j) != w[0] || cal.Dist2(pent[i], pent[j]) != w[1] || cal.Dist2(pent[(i+1)%5], pent[j]) != w[2] {
			ok = false
		}
	}
	check("cal 五条边对跖顶点与两个 d² 符合手算表", ok)

	// rc：五边形直径与矩形并列
	d2, pairs := rc.New(pent).Diameter()
	check("rc 五边形直径 d²=52 对=(0,2)", d2 == 52 && len(pairs) == 1 && pairs[0] == [2]int{0, 2})
	d2, pairs = rc.New([]cal.Point{{X: 0, Y: 0}, {X: 6, Y: 0}, {X: 6, Y: 4}, {X: 0, Y: 4}}).Diameter()
	check("rc 矩形 d²=52 并列两对 (0,2),(1,3)",
		d2 == 52 && len(pairs) == 2 && pairs[0] == [2]int{0, 2} && pairs[1] == [2]int{1, 3})

	// api：装载 + 自检四条不变量
	if err := api.New(pent); err != nil {
		check("api New 五边形", false)
	}
	d2, pairs, err := api.Diameter()
	check("api 直径 d²=52 对=(0,2)", err == nil && d2 == 52 && len(pairs) == 1 && pairs[0] == [2]int{0, 2})
	check("api SelfCheck 四条不变量", api.SelfCheck() == nil)

	// api：三类可判定错误互不相同
	e1 := api.New([]cal.Point{{X: 0, Y: 0}, {X: 1, Y: 1}})
	e2 := api.New([]cal.Point{{X: 0, Y: 0}, {X: 20000, Y: 0}, {X: 0, Y: 5}})
	e3 := api.New([]cal.Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 4, Y: 4}, {X: 2, Y: 2}})
	check("api 三类可判定错误互不相同",
		errors.Is(e1, api.ErrTooFewVertices) && errors.Is(e2, api.ErrOutOfRange) && errors.Is(e3, api.ErrNotConvex) &&
			!errors.Is(e1, e2) && !errors.Is(e2, e3) && !errors.Is(e1, e3))

	// api：被拒后无部分结果，仍可用
	d2, pairs, err = api.Diameter()
	check("api 被拒后无部分结果", err == nil && d2 == 52 && len(pairs) == 1 && pairs[0] == [2]int{0, 2})

	// rc：m=100..10000 各档与暴力一致（计数随 m 线性由 rc 包测试钉住）
	ok = true
	for _, m := range []int{100, 1000, 10000} {
		p := regularish(m, float64(m)*100)
		if d, _ := rc.New(p).Diameter(); d != brute(p) {
			ok = false
		}
	}
	check("rc m=100..10000 与暴力一致（计数线性见 rc 测试）", ok)

	// api：并发只读结果一致
	const G, T = 8, 200
	start := make(chan struct{})
	var wg sync.WaitGroup
	var bad atomic.Bool
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for t := 0; t < T; t++ {
				d, ps, err := api.Diameter()
				if err != nil || d != 52 || len(ps) != 1 || ps[0] != [2]int{0, 2} || api.SelfCheck() != nil {
					bad.Store(true)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	check("api 并发只读结果一致", !bad.Load())

	if failed {
		os.Exit(1)
	}
}
