// demo 逐项验证最小包围圆实现，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/circ"
	"ontology/mec"
)

var failed atomic.Bool

func check(name string, ok bool) {
	if !ok {
		failed.Store(true)
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK  ", false: "FAIL"}[ok], name)
}

func pt(x, y int) circ.Point { return circ.Point{X: x, Y: y} }

func ratOf(s string) *big.Rat { r, _ := new(big.Rat).SetString(s); return r }

func eq(c circ.Circle, cx, cy, r2 string) bool {
	return c.Cx.Cmp(ratOf(cx)) == 0 && c.Cy.Cmp(ratOf(cy)) == 0 && c.R2.Cmp(ratOf(r2)) == 0
}

func same(a, b circ.Circle) bool {
	return a.Cx.Cmp(b.Cx) == 0 && a.Cy.Cmp(b.Cy) == 0 && a.R2.Cmp(b.R2) == 0
}

// brute 独立暴力枚举：单点、点对直径、三点圆（含回退），取覆盖全部点的最小者。
func brute(pts []circ.Point) circ.Circle {
	var best circ.Circle
	have := false
	try := func(c circ.Circle) {
		for _, p := range pts {
			if !c.Contains(p) {
				return
			}
		}
		if !have || c.R2.Cmp(best.R2) < 0 {
			best, have = c, true
		}
	}
	for i, a := range pts {
		try(circ.FromPoint(a))
		for j := i + 1; j < len(pts); j++ {
			try(circ.FromDiameter(a, pts[j]))
			for k := j + 1; k < len(pts); k++ {
				try(circ.FromThree(a, pts[j], pts[k]))
			}
		}
	}
	return best
}

func main() {
	o, p6 := pt(0, 0), pt(6, 0)
	ok := eq(circ.FromPoint(o), "0", "0", "0") && len(circ.FromPoint(o).B) == 1 &&
		eq(circ.FromDiameter(o, p6), "3", "0", "9") && len(circ.FromDiameter(o, p6).B) == 2 &&
		eq(circ.FromThree(o, p6, pt(0, 8)), "3", "4", "25") && len(circ.FromThree(o, p6, pt(0, 8)).B) == 3 &&
		eq(circ.FromThree(o, p6, pt(1, 1)), "3", "0", "9") && len(circ.FromThree(o, p6, pt(1, 1)).B) == 2 &&
		eq(circ.FromThree(o, pt(3, 0), p6), "3", "0", "9") && len(circ.FromThree(o, pt(3, 0), p6).B) == 2
	check("S1..S5 圆心与半径", ok)
	// 钝角回退：S4 不得取外接圆（外接圆心 (3,-2)、r²=13）；共线回退：S5 不得除零。
	c4 := circ.FromThree(o, p6, pt(1, 1))
	check("钝角回退", eq(c4, "3", "0", "9") && c4.Contains(pt(1, 1)) && !c4.OnBoundary(pt(1, 1)))
	c5 := circ.FromThree(o, pt(3, 0), p6)
	check("共线回退", eq(c5, "3", "0", "9") && c5.OnBoundary(o) && c5.OnBoundary(p6))
	rng := rand.New(rand.NewSource(771))
	agree, cover := true, true
	for t := 0; t < 60 && agree && cover; t++ {
		seen := map[circ.Point]bool{}
		var pts []circ.Point
		for n := 1 + t%12; len(pts) < n; {
			if q := pt(rng.Intn(81)-40, rng.Intn(81)-40); !seen[q] {
				seen[q], pts = true, append(pts, q)
			}
		}
		svc, _ := api.New()
		for _, q := range pts {
			agree = agree && svc.Insert(q.X, q.Y) == nil
		}
		got, err := svc.MinCircle()
		agree = agree && err == nil && same(got, brute(pts))
		for _, q := range pts {
			cover = cover && got.Contains(q)
		}
		for _, q := range svc.Boundary() {
			cover = cover && got.OnBoundary(q)
		}
	}
	check("与暴力枚举一致", agree)
	check("最小性与覆盖正确", cover)
	svc, _ := api.New()
	_, e0 := svc.MinCircle()
	_ = svc.Insert(1, 1)
	e1, e2 := svc.Insert(1, 1), svc.Insert(10001, 0)
	check("三类可判定错误", errors.Is(e0, api.ErrEmpty) && errors.Is(e1, api.ErrDuplicate) &&
		errors.Is(e2, api.ErrOutOfRange) && !errors.Is(e1, api.ErrOutOfRange) && !errors.Is(e2, api.ErrDuplicate))
	before, _ := svc.MinCircle()
	nb := len(svc.Boundary())
	_ = svc.Insert(1, 1)
	_ = svc.Insert(-10001, 0)
	after, _ := svc.MinCircle()
	ok = same(after, before) && len(svc.Boundary()) == nb && svc.Insert(2, 2) == nil
	check("被拒后状态不变", ok)
	check("大 m 插入判定恒为 1", mec.InteriorProbe(100) && mec.InteriorProbe(1000) && mec.InteriorProbe(10000))

	// 并发只读：多 goroutine 读到的圆心、r²、边界逐字段相同。
	svc2, _ := api.New()
	for _, q := range []circ.Point{o, p6, pt(0, 8), pt(-3, -4), pt(10, 2)} {
		_ = svc2.Insert(q.X, q.Y)
	}
	want, _ := svc2.MinCircle()
	wantB := svc2.Boundary()
	var wg sync.WaitGroup
	var conBad atomic.Bool
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				c, err := svc2.MinCircle()
				ok := err == nil && same(c, want) && svc2.SelfCheck() == nil &&
					slices.Equal(svc2.Boundary(), wantB)
				if !ok {
					conBad.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	check("并发只读一致", !conBad.Load())

	svc3, _ := api.New()
	check("SelfCheck", svc3.SelfCheck() == nil)
	if failed.Load() {
		os.Exit(1)
	}
}
