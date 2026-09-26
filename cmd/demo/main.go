package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/meas"
	"ontology/poly"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func lshape() []api.Point {
	return []api.Point{{X: 0, Y: 0}, {X: 6, Y: 0}, {X: 6, Y: 2}, {X: 2, Y: 2}, {X: 2, Y: 6}, {X: 0, Y: 6}}
}

func main() {
	l := lshape()
	pg, err := api.New(l)
	if err != nil {
		fmt.Println("FAIL build:", err)
		os.Exit(1)
	}

	// 第三节六行表：逐边叉积与矩项
	wantC := []int64{0, 12, 8, 8, 12, 0}
	wantMX := []int64{0, 144, 64, 32, 24, 0}
	wantMY := []int64{0, 24, 32, 64, 144, 0}
	edgesOK := true
	for i := range l {
		a, b := l[i], l[(i+1)%len(l)]
		c := poly.Cross(a, b)
		if c != wantC[i] || (a.X+b.X)*c != wantMX[i] || (a.Y+b.Y)*c != wantMY[i] {
			edgesOK = false
		}
	}
	check("六条边叉积与矩项", edgesOK)

	area, _ := pg.Area()
	cen, _ := pg.Centroid()
	check("面积 20、重心 (11/5,11/5)", meas.Eq(area, meas.NewRat(20, 1)) &&
		meas.Eq(cen.X, meas.NewRat(11, 5)) && meas.Eq(cen.Y, meas.NewRat(11, 5)))

	cw := []api.Point{{X: 0, Y: 0}, {X: 0, Y: 6}, {X: 6, Y: 0}}
	_, errCW := api.New(cw)
	check("顺时针被拒(ErrNotCCW)", errors.Is(errCW, api.ErrNotCCW))

	bads := []struct {
		v    []api.Point
		want error
	}{
		{[]api.Point{{X: 0, Y: 0}, {X: 4, Y: 4}, {X: 4, Y: 0}, {X: 0, Y: 4}}, api.ErrSelfIntersect},
		{cw, api.ErrNotCCW},
		{[]api.Point{{X: 0, Y: 0}, {X: 1, Y: 1}}, api.ErrTooFewVertices},
		{[]api.Point{{X: 0, Y: 0}, {X: 20000, Y: 0}, {X: 0, Y: 1}}, api.ErrOutOfRange},
		{[]api.Point{{X: 0, Y: 0}, {X: 2, Y: 0}, {X: 2, Y: 0}, {X: 0, Y: 2}}, api.ErrDuplicateVertex},
	}
	errsOK, noPartial := true, true
	for _, b := range bads {
		p, err := api.New(b.v)
		if !errors.Is(err, b.want) {
			errsOK = false
		}
		if p != nil {
			noPartial = false
		}
	}
	check("五类可判定错误", errsOK)
	check("被拒后无部分结果", noPartial)

	// 与三角剖分一致：原点扇形逐三角形有理数加权
	zero := meas.NewRat(0, 1)
	numX, numY, den := zero, zero, zero
	for i := range l {
		a, b := l[i], l[(i+1)%len(l)]
		w := meas.NewRat(poly.Cross(a, b), 2)
		numX = meas.Add(numX, meas.Mul(w, meas.NewRat(a.X+b.X, 3)))
		numY = meas.Add(numY, meas.Mul(w, meas.NewRat(a.Y+b.Y, 3)))
		den = meas.Add(den, w)
	}
	triX := meas.NewRat(numX.Num*den.Den, numX.Den*den.Num)
	triY := meas.NewRat(numY.Num*den.Den, numY.Den*den.Num)
	check("与三角剖分一致", meas.Eq(cen.X, triX) && meas.Eq(cen.Y, triY))

	mv := make([]api.Point, len(l))
	for i, q := range l {
		mv[i] = api.Point{X: q.X + 7, Y: q.Y - 3}
	}
	mp, errM := api.New(mv)
	mc, _ := mp.Centroid()
	check("平移不变", errM == nil &&
		meas.Eq(mc.X, meas.Add(cen.X, meas.NewRat(7, 1))) &&
		meas.Eq(mc.Y, meas.Add(cen.Y, meas.NewRat(-3, 1))))

	mean := meas.NewRat(8, 3) // 顶点均值 (8/3,8/3)
	diff := meas.Add(cen.X, meas.NewRat(-8, 3))
	check("顶点均值差异 -7/15", !meas.Eq(cen.X, mean) && meas.Eq(diff, meas.NewRat(-7, 15)))

	check("m 边形计数=n", meas.VerifyLinearReads(100, 500, 1000, 5000, 10000))

	var wg sync.WaitGroup
	bad := make(chan struct{}, 32)
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				a, _ := pg.Area()
				c, _ := pg.Centroid()
				if !meas.Eq(a, area) || !meas.Eq(c.X, cen.X) || !meas.Eq(c.Y, cen.Y) || pg.SelfCheck() != nil {
					bad <- struct{}{}
					return
				}
			}
		}()
	}
	wg.Wait()
	close(bad)
	_, raceBad := <-bad
	check("并发只读一致+SelfCheck", !raceBad)

	if failed {
		os.Exit(1)
	}
}
