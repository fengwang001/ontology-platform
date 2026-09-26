package main

import (
	"fmt"
	"math/big"
	"os"
	"sync"

	"ontology/api"
	"ontology/circ"
	"ontology/mec"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func pt(x, y int) circ.Point { return circ.Point{X: x, Y: y} }

func circleIs(c circ.Circle, cx, cy, r2num, r2den int64, nb int) bool {
	return c.Cx.Cmp(big.NewRat(cx, 1)) == 0 &&
		c.Cy.Cmp(big.NewRat(cy, 1)) == 0 &&
		c.R2.Cmp(big.NewRat(r2num, r2den)) == 0 &&
		len(c.B) == nb
}

func main() {
	check("S1 单点圆 圆心(0,0) r=0", circleIs(circ.FromPoint(pt(0, 0)), 0, 0, 0, 1, 1))
	check("S2 直径圆 圆心(3,0) r=3", circleIs(circ.FromDiameter(pt(0, 0), pt(6, 0)), 3, 0, 9, 1, 2))
	check("S3 直角回退 圆心(3,4) r=5", circleIs(circ.FromThree(pt(0, 0), pt(6, 0), pt(0, 8)), 3, 4, 25, 1, 2))
	check("S4 钝角回退 圆心(3,0) r=3", circleIs(circ.FromThree(pt(0, 0), pt(6, 0), pt(1, 1)), 3, 0, 9, 1, 2))
	check("S5 共线回退 圆心(3,0) r=3", circleIs(circ.FromThree(pt(0, 0), pt(3, 0), pt(6, 0)), 3, 0, 9, 1, 2))

	sets := [][]circ.Point{
		{pt(0, 0)}, {pt(0, 0), pt(6, 0)},
		{pt(0, 0), pt(6, 0), pt(0, 8)}, {pt(0, 0), pt(6, 0), pt(1, 1)},
		{pt(0, 0), pt(3, 0), pt(6, 0)},
		{pt(3, 1), pt(-4, 2), pt(5, -3), pt(0, 7), pt(-2, -6)},
	}
	ok := true
	for _, s := range sets {
		m := mec.New()
		for _, p := range s {
			m.Insert(p)
		}
		got, _ := m.Circle()
		if !got.Equal(mec.BruteForce(s)) {
			ok = false
		}
		for _, p := range s {
			ok = ok && got.Contains(p)
		}
		for _, b := range got.B {
			ok = ok && got.OnBoundary(b)
		}
	}
	a, _ := api.New()
	check("暴力枚举一致/最小性/覆盖正确/SelfCheck", ok && a.SelfCheck() == nil)

	_, e1 := a.MinCircle()
	_ = a.Insert(0, 0)
	_ = a.Insert(6, 0)
	before, _ := a.MinCircle()
	e2, e3 := a.Insert(0, 0), a.Insert(10001, 0)
	check("三类可判定错误互不相同", e1 == api.ErrEmpty && e2 == api.ErrDuplicate &&
		e3 == api.ErrOutOfRange && e1 != e2 && e2 != e3 && e1 != e3)
	after, _ := a.MinCircle()
	check("被拒后状态不变", before.Cx.Cmp(after.Cx) == 0 && before.R2.Cmp(after.R2) == 0 &&
		len(a.Boundary()) == 2 && a.Insert(3, 3) == nil)

	b, _ := api.New()
	_ = b.Insert(0, 0)
	_ = b.Insert(1000, 0)
	c0, _ := b.MinCircle()
	for i := 0; i < 10000; i++ { // 全部严格落在圆内
		_ = b.Insert(400+i%201, i/201)
	}
	c1, _ := b.MinCircle()
	check("大 m 圆内插入 O(1) 圆不变", c0.Cx.Cmp(c1.Cx) == 0 && c0.R2.Cmp(c1.R2) == 0)

	want, _ := b.MinCircle()
	var wg sync.WaitGroup
	bad := make(chan bool, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				g, err := b.MinCircle()
				if err != nil || g.Cx.Cmp(want.Cx) != 0 || g.R2.Cmp(want.R2) != 0 || len(b.Boundary()) != 2 {
					bad <- true
					return
				}
			}
		}()
	}
	wg.Wait()
	close(bad)
	check("并发只读结果一致", len(bad) == 0)

	if failed {
		os.Exit(1)
	}
}
