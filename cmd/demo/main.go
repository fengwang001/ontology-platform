package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/meas"
	"ontology/poly"
)

var failed bool

func check(ok bool, name string) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func pt(x, y int64) poly.Point { return poly.Point{X: x, Y: y} }

// lshape 是第三节的 L 形凹多边形（逆时针）。
func lshape() []poly.Point {
	return []poly.Point{pt(0, 0), pt(6, 0), pt(6, 2), pt(2, 2), pt(2, 6), pt(0, 6)}
}

func reverse(v []poly.Point) []poly.Point {
	r := make([]poly.Point, len(v))
	for i, p := range v {
		r[len(v)-1-i] = p
	}
	return r
}

// ngon 生成半径 5000 圆上的 m 个整数点（逆时针简单多边形）。
func ngon(m int) []poly.Point {
	v := make([]poly.Point, m)
	for i := range v {
		t := 2 * math.Pi * float64(i) / float64(m)
		v[i] = pt(int64(math.Round(5000*math.Cos(t))), int64(math.Round(5000*math.Sin(t))))
	}
	return v
}

func main() {
	v := lshape()
	pg, err := api.New(v)
	a, _ := pg.Area()
	c, _ := pg.Centroid()

	// 1. 第三节：六条边的叉积与两个矩项。
	want := []int64{0, 12, 8, 8, 12, 0}
	var sc, smx, smy int64
	ok := err == nil
	for i, w := range want {
		ci := poly.Cross(v[i], v[(i+1)%6])
		ok = ok && ci == w
		sc += ci
		smx += (v[i].X + v[(i+1)%6].X) * ci
		smy += (v[i].Y + v[(i+1)%6].Y) * ci
	}
	check(ok && sc == 40 && smx == 264 && smy == 264, "六边叉积 0,12,8,8,12,0，矩项 Σ=(264,264)")

	// 2. 面积与重心。
	check(a == meas.NewRat(20, 1) && c.X == meas.NewRat(11, 5) && c.Y == meas.NewRat(11, 5),
		"面积 20、重心 (11/5,11/5)")

	// 3. 自检：三角剖分一致 / 平移不变 / 面积真 / 拒绝不留痕。
	check(api.SelfCheck() == nil, "自检：三角剖分一致、平移不变、面积真、拒绝不留痕")

	// 4. 顶点均值 (8/3,8/3) ≠ 重心 (11/5,11/5)，差 7/15。
	var sx, sy int64
	for _, p := range v {
		sx += p.X
		sy += p.Y
	}
	mean := meas.NewRat(sx, 6)
	diff := meas.NewRat(mean.Num*c.X.Den-c.X.Num*mean.Den, mean.Den*c.X.Den)
	check(mean == meas.NewRat(8, 3) && sy == 16 && mean != c.X && diff == meas.NewRat(7, 15),
		"顶点均值 (8/3,8/3)≠重心 (11/5,11/5)，差 7/15")

	// 5. 顺时针被拒；四类错误可判定且互不相同；被拒后无部分结果且可继续用。
	bad := []struct {
		v    []poly.Point
		want error
	}{
		{[]poly.Point{pt(0, 0), pt(4, 4), pt(4, 0), pt(0, 4)}, api.ErrSelfIntersect},
		{reverse(v), api.ErrNotCCW},
		{[]poly.Point{pt(0, 0), pt(1, 1)}, api.ErrTooFewVertices},
		{[]poly.Point{pt(0, 0), pt(20000, 0), pt(0, 3)}, api.ErrOutOfRange},
	}
	seen := map[error]bool{}
	ok = true
	for _, b := range bad {
		r, e := api.New(b.v)
		ok = ok && r == nil && errors.Is(e, b.want) && !seen[e]
		seen[e] = true
	}
	a2, _ := pg.Area()
	check(ok && a2 == a, "顺时针被拒；四类错误互不相同；被拒后无部分结果")

	// 6. m 边形计数恰好等于 n（反射读非导出字段，公开接口拿不到）。
	ok = true
	for _, m := range []int{100, 1000, 10000} {
		mp := meas.NewPolygon(ngon(m))
		_ = mp.Area()
		got := reflect.ValueOf(mp).Elem().FieldByName("reads").Int()
		ok = ok && got == int64(m)
	}
	check(ok, "m∈{100,1000,10000} 计数==n")

	// 7. 并发只读：16 goroutine × 50 次，结果逐字段相同。
	var wg sync.WaitGroup
	var bad32 int32
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				ga, _ := pg.Area()
				gc, _ := pg.Centroid()
				if ga != a || gc != c {
					atomic.StoreInt32(&bad32, 1)
				}
			}
		}()
	}
	wg.Wait()
	check(atomic.LoadInt32(&bad32) == 0 && api.SelfCheck() == nil, "并发只读结果一致")

	if failed {
		os.Exit(1)
	}
}
