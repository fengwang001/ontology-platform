package api_test

import (
	"errors"
	"math"
	"math/rand"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/meas"
	"ontology/poly"
)

func pt(x, y int64) api.Point { return api.Point{X: x, Y: y} }

var lshape = []api.Point{pt(0, 0), pt(6, 0), pt(6, 2), pt(2, 2), pt(2, 6), pt(0, 6)}

// randSimple 固定种子生成随机简单多边形：角度递增绕原点一圈 ⇒ 简单且逆时针。
func randSimple(seed int64, m int) []api.Point {
	r := rand.New(rand.NewSource(seed))
	ang := make([]float64, m)
	for i := range ang {
		ang[i] = r.Float64()
	}
	slices.Sort(ang)
	v := make([]api.Point, m)
	for i, a := range ang {
		rr := 1000 + r.Float64()*8000
		v[i] = pt(int64(math.Round(rr*math.Cos(2*math.Pi*a))), int64(math.Round(rr*math.Sin(2*math.Pi*a))))
	}
	return v
}

// fanCentroid 测试内独立实现：v0 扇形三角剖分、面积加权平均。
func fanCentroid(v []api.Point) api.PointRat {
	var sx, sy, sw int64
	for i := 1; i+1 < len(v); i++ {
		v0, a, b := v[0], v[i], v[i+1]
		w := (a.X-v0.X)*(b.Y-v0.Y) - (a.Y-v0.Y)*(b.X-v0.X)
		sw, sx, sy = sw+w, sx+w*(v0.X+a.X+b.X), sy+w*(v0.Y+a.Y+b.Y)
	}
	return api.PointRat{X: meas.NewRat(sx, 3*sw), Y: meas.NewRat(sy, 3*sw)}
}

var sets = [][]api.Point{lshape, {pt(0, 0), pt(4, 1), pt(3, 5), pt(-1, 4)},
	{pt(-3, -2), pt(5, -1), pt(1, 4)}, randSimple(1, 7), randSimple(2, 23)}

func mustNew(t *testing.T, v []api.Point) *api.Polygon {
	t.Helper()
	pg, err := api.New(v)
	if err != nil {
		t.Fatal(err)
	}
	return pg
}

func TestCentroidMatchesTriangulation(t *testing.T) { // 不变量 1
	for i, v := range sets {
		c, _ := mustNew(t, v).Centroid()
		if want := fanCentroid(v); c != want {
			t.Fatalf("set %d: 重心 %v ≠ 三角剖分加权 %v", i, c, want)
		}
	}
}
func TestCentroidTranslationInvariant(t *testing.T) { // 不变量 2
	for _, sh := range []api.Point{pt(7, -3), pt(-100, 250), pt(0, 0)} {
		for i, v := range sets {
			c0, _ := mustNew(t, v).Centroid()
			mv := make([]api.Point, len(v))
			for j, p := range v {
				mv[j] = pt(p.X+sh.X, p.Y+sh.Y)
			}
			c1, _ := mustNew(t, mv).Centroid()
			if c1.X != meas.NewRat(c0.X.Num+sh.X*c0.X.Den, c0.X.Den) ||
				c1.Y != meas.NewRat(c0.Y.Num+sh.Y*c0.Y.Den, c0.Y.Den) {
				t.Fatalf("set %d shift %v: %v 未随平移", i, sh, c1)
			}
		}
	}
}

func TestAreaPositiveAndRejectsCW(t *testing.T) { // 不变量 3
	for i, v := range sets {
		if a, _ := mustNew(t, v).Area(); a.Num <= 0 || a != meas.NewRat(poly.SignedArea2(v), 2) {
			t.Fatalf("set %d: 面积 %v 非正或不真", i, a)
		}
		rev := make([]api.Point, len(v))
		for j, p := range v {
			rev[len(v)-1-j] = p
		}
		if r, err := api.New(rev); !errors.Is(err, api.ErrNotCCW) || r != nil {
			t.Fatalf("set %d: 顺时针应拒为 ErrNotCCW，got %v", i, err)
		}
	}
}

func TestRejectedInputLeavesNoPartialResult(t *testing.T) { // 不变量 4
	bad := []struct {
		v    []api.Point
		want error
	}{
		{[]api.Point{pt(0, 0), pt(4, 4), pt(4, 0), pt(0, 4)}, api.ErrSelfIntersect},
		{[]api.Point{pt(0, 0), pt(0, 2), pt(2, 0)}, api.ErrNotCCW},
		{[]api.Point{pt(0, 0), pt(1, 1)}, api.ErrTooFewVertices},
		{[]api.Point{pt(0, 0), pt(10001, 0), pt(0, 3)}, api.ErrOutOfRange},
		{[]api.Point{pt(0, 0), pt(2, 0), pt(2, 2), pt(0, 0)}, api.ErrDuplicateVertex},
	}
	for i, b := range bad {
		r, err := api.New(b.v)
		if r != nil || !errors.Is(err, b.want) {
			t.Fatalf("case %d: got (%v,%v)，want (nil,%v)", i, r, err, b.want)
		}
		for j, b2 := range bad { // 互不相同：不得被判为其他类
			if i != j && errors.Is(err, b2.want) {
				t.Fatalf("case %d 与 case %d 不可区分", i, j)
			}
		}
	}
	if a, _ := mustNew(t, lshape).Area(); a != meas.NewRat(20, 1) { // 被拒后仍可正常用
		t.Fatalf("拒绝后既有对象异常：面积 %v", a)
	}
}

func TestConcurrentReadOnly(t *testing.T) {
	pg := mustNew(t, lshape)
	a, _ := pg.Area()
	c, _ := pg.Centroid()
	var wg sync.WaitGroup
	var bad int32
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				ga, _ := pg.Area()
				gc, _ := pg.Centroid()
				if ga != a || gc != c || api.SelfCheck() != nil {
					atomic.StoreInt32(&bad, 1)
				}
			}
		}()
	}
	wg.Wait()
	if atomic.LoadInt32(&bad) != 0 {
		t.Fatal("并发只读结果不一致")
	}
}
