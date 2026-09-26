package api

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/meas"
	"ontology/poly"
)

func lshape() []Point {
	return []Point{{X: 0, Y: 0}, {X: 6, Y: 0}, {X: 6, Y: 2}, {X: 2, Y: 2}, {X: 2, Y: 6}, {X: 0, Y: 6}}
}

// genArch 生成 m 顶点逆时针严格凸链（抛物线拱，坐标 ≤ m²/4 ≤ 1e4）。
func genArch(m int) []Point {
	v := make([]Point, m)
	for i := range v {
		x := int64(m - 1 - i)
		v[i] = Point{X: x, Y: x * (int64(m) - x)}
	}
	return v
}

func mustNew(t *testing.T, v []Point) *Polygon {
	t.Helper()
	p, err := New(v)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestAreaPositiveAndTrue(t *testing.T) { // 不变量 3 + 第三节(乙)(丙)
	cases := [][]Point{lshape(), {{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 0, Y: 3}}, genArch(50), genArch(200)}
	for _, v := range cases {
		a, err := mustNew(t, v).Area()
		if err != nil || a.Num <= 0 {
			t.Fatalf("area not positive: %v %v", a, err)
		}
		if want := meas.NewRat(poly.SignedArea2(v), 2); !meas.Eq(a, want) {
			t.Errorf("area %v != |signed| %v", a, want)
		}
	}
	p := mustNew(t, lshape())
	a, _ := p.Area()
	c, _ := p.Centroid()
	if !meas.Eq(a, meas.NewRat(20, 1)) || !meas.Eq(c.X, meas.NewRat(11, 5)) || !meas.Eq(c.Y, meas.NewRat(11, 5)) {
		t.Errorf("L: area %v centroid %v, want 20, (11/5,11/5)", a, c)
	}
	if meas.Eq(c.X, meas.NewRat(8, 3)) { // 顶点均值 (8/3,8/3) 不是重心
		t.Error("centroid equals vertex mean")
	}
	if d := meas.Add(c.X, meas.NewRat(-8, 3)); !meas.Eq(d, meas.NewRat(-7, 15)) {
		t.Errorf("centroid-mean = %v, want -7/15", d)
	}
}

func TestTriangulationConsistent(t *testing.T) { // 不变量 1
	rng := rand.New(rand.NewSource(7))
	cases := [][]Point{lshape(), genArch(3), genArch(10), genArch(100)}
	for i := 0; i < 5; i++ { // 随机规模简单多边形，循环生成
		cases = append(cases, genArch(3+rng.Intn(150)))
	}
	for _, v := range cases {
		c, _ := mustNew(t, v).Centroid()
		tri := triangulatedCentroid(v)
		if !meas.Eq(c.X, tri.X) || !meas.Eq(c.Y, tri.Y) {
			t.Errorf("n=%d: centroid %v != triangulation %v", len(v), c, tri)
		}
	}
}

func TestTranslationInvariant(t *testing.T) { // 不变量 2
	for _, v := range [][]Point{lshape(), genArch(20)} {
		c0, _ := mustNew(t, v).Centroid()
		for _, s := range []Point{{X: 7, Y: -3}, {X: 100, Y: 200}} {
			mv := make([]Point, len(v))
			for i, q := range v {
				mv[i] = Point{X: q.X + s.X, Y: q.Y + s.Y}
			}
			mp, err := New(mv)
			if err != nil {
				t.Fatalf("shifted New: %v", err)
			}
			mc, _ := mp.Centroid()
			if !meas.Eq(mc.X, meas.Add(c0.X, meas.NewRat(s.X, 1))) || !meas.Eq(mc.Y, meas.Add(c0.Y, meas.NewRat(s.Y, 1))) {
				t.Errorf("shift %v: %v, want %v+shift", s, mc, c0)
			}
		}
	}
}

func TestRejectNoPartial(t *testing.T) { // 不变量 4 + 五类可判定错误互不相同
	sents := []error{ErrTooFewVertices, ErrOutOfRange, ErrDuplicateVertex, ErrSelfIntersect, ErrNotCCW}
	for i, a := range sents {
		for j, b := range sents {
			if i != j && errors.Is(a, b) {
				t.Errorf("sentinels %d,%d not distinct", i, j)
			}
		}
	}
	cases := []struct {
		v    []Point
		want error
	}{
		{[]Point{{X: 0, Y: 0}, {X: 1, Y: 1}}, ErrTooFewVertices},
		{[]Point{{X: 0, Y: 0}, {X: 20000, Y: 0}, {X: 0, Y: 1}}, ErrOutOfRange},
		{[]Point{{X: 0, Y: 0}, {X: 2, Y: 0}, {X: 2, Y: 0}, {X: 0, Y: 2}}, ErrDuplicateVertex},
		{[]Point{{X: 0, Y: 0}, {X: 4, Y: 4}, {X: 4, Y: 0}, {X: 0, Y: 4}}, ErrSelfIntersect},
		{[]Point{{X: 0, Y: 0}, {X: 0, Y: 6}, {X: 6, Y: 0}}, ErrNotCCW},
	}
	for _, tc := range cases {
		if p, err := New(tc.v); !errors.Is(err, tc.want) || p != nil {
			t.Errorf("New = (%v,%v), want (nil,%v)", p, err, tc.want)
		}
	}
	if err := mustNew(t, lshape()).SelfCheck(); err != nil { // 被拒后仍可正常使用
		t.Errorf("usable after rejects: %v", err)
	}
}

func TestConcurrentReadOnly(t *testing.T) { // 并发只读，结果逐字段相同
	p := mustNew(t, genArch(100))
	refA, _ := p.Area()
	refC, _ := p.Centroid()
	var wg sync.WaitGroup
	errs := make(chan string, 64) // 每 goroutine 至多发一条
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				a, _ := p.Area()
				c, _ := p.Centroid()
				if !meas.Eq(a, refA) || !meas.Eq(c.X, refC.X) || !meas.Eq(c.Y, refC.Y) || p.SelfCheck() != nil {
					errs <- "mismatch"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
