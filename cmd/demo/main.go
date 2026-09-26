package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/cal"
	"ontology/rc"
)

func pt(x, y int64) api.Point { return api.Point{X: x, Y: y} }

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Println(map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func brute(poly []api.Point) (int64, map[[2]int]bool) {
	best, out := int64(-1), map[[2]int]bool{}
	for i := range poly {
		for j := i + 1; j < len(poly); j++ {
			if d := cal.Dist2(poly[i], poly[j]); d >= best {
				if d > best {
					best, out = d, map[[2]int]bool{}
				}
				out[[2]int{i, j}] = true
			}
		}
	}
	return best, out
}

func antipodal(poly []api.Point, a, b int) bool {
	n := len(poly)
	abs := func(v int64) int64 {
		if v < 0 {
			return -v
		}
		return v
	}
	for _, t := range [4][2]int{{a, b}, {(a - 1 + n) % n, b}, {b, a}, {(b - 1 + n) % n, a}} {
		x, y := poly[t[0]], poly[(t[0]+1)%n]
		m, ok := abs(cal.Area2(x, y, poly[t[1]])), true
		for k := 0; k < n && ok; k++ {
			ok = abs(cal.Area2(x, y, poly[k])) <= m
		}
		if ok {
			return true
		}
	}
	return false
}

func main() {
	pent := []api.Point{pt(0, 0), pt(5, 1), pt(6, 4), pt(3, 6), pt(1, 5)}
	rect := []api.Point{pt(0, 0), pt(6, 0), pt(6, 4), pt(0, 4)}
	hexa := []api.Point{pt(0, 0), pt(4, 1), pt(6, 5), pt(3, 8), pt(-1, 6), pt(-2, 3)}

	ok := true
	wantAnt := []int{3, 4, 0, 1, 2}
	wantD2 := [][2]int64{{45, 29}, {32, 26}, {52, 45}, {29, 32}, {26, 52}}
	for i := range pent {
		j := cal.Antipodal(pent, i)
		ok = ok && j == wantAnt[i] &&
			cal.Dist2(pent[i], pent[j]) == wantD2[i][0] &&
			cal.Dist2(pent[(i+1)%len(pent)], pent[j]) == wantD2[i][1]
	}
	check("五边形五条边对跖顶点与两个 d²", ok)

	d2, pairs := rc.Diameter(pent)
	check("五边形直径 d²=52 且由 (0,2) 取得", d2 == 52 && reflect.DeepEqual(pairs, [][2]int{{0, 2}}))
	d2r, pairsR := rc.Diameter(rect)
	check("矩形直径 52 且并列两对", d2r == 52 && reflect.DeepEqual(pairsR, [][2]int{{0, 2}, {1, 3}}))

	ok1, ok2, ok3 := true, true, true
	for _, poly := range [][]api.Point{pent, rect, hexa} {
		var g api.Polygon
		if g.New(poly) != nil {
			ok1, ok2, ok3 = false, false, false
			continue
		}
		d, prs, err := g.Diameter()
		best, maxPairs := brute(poly)
		ok1 = ok1 && err == nil && d == best
		for _, pr := range prs {
			ok2 = ok2 && antipodal(poly, pr[0], pr[1]) && cal.Dist2(poly[pr[0]], poly[pr[1]]) == d
			delete(maxPairs, pr)
		}
		ok3 = ok3 && len(maxPairs) == 0
	}
	check("与暴力枚举一致", ok1)
	check("直径在对跖对上", ok2)
	check("并列完整", ok3)

	var g api.Polygon
	e1 := g.New([]api.Point{pt(0, 0), pt(4, 0), pt(2, 2), pt(4, 4), pt(0, 4)}) // 非凸
	e2 := g.New([]api.Point{pt(0, 0), pt(1, 1)})                               // 顶点不足
	e3 := g.New([]api.Point{pt(0, 0), pt(10001, 0), pt(0, 5)})                 // 坐标越界
	check("三类可判定错误互不相同", errors.Is(e1, api.ErrNotConvex) &&
		errors.Is(e2, api.ErrTooFewVertices) && errors.Is(e3, api.ErrOutOfRange) &&
		!errors.Is(e1, api.ErrTooFewVertices) && !errors.Is(e1, api.ErrOutOfRange) &&
		!errors.Is(e2, api.ErrOutOfRange))

	d0, pr0, err0 := g.Diameter()
	ok = errors.Is(err0, api.ErrNotInitialized) && d0 == 0 && pr0 == nil // 三次 New 全失败，g 仍是零值
	ok = ok && g.New(pent) == nil
	dOK, prOK, _ := g.Diameter()
	ok = ok && g.New([]api.Point{pt(0, 0), pt(1, 1)}) != nil // 被拒后状态不变
	d1, pr1, _ := g.Diameter()
	check("被拒后无部分结果且可继续用", ok && d1 == dOK && reflect.DeepEqual(pr1, prOK))

	check("m 边形计数随 m 线性", rc.SelfCheck() == nil && g.SelfCheck() == nil)

	const N = 32
	ds, prs := make([]int64, N), make([][][2]int, N)
	var wg sync.WaitGroup
	for k := 0; k < N; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ds[k], prs[k], _ = g.Diameter()
			_ = g.SelfCheck()
		}()
	}
	wg.Wait()
	ok = true
	for k := 1; k < N; k++ {
		ok = ok && ds[k] == ds[0] && reflect.DeepEqual(prs[k], prs[0])
	}
	check("并发只读结果一致", ok)

	if failed {
		os.Exit(1)
	}
}
