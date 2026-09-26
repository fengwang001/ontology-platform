package api

import (
	"errors"
	"math"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/cvex"
)

func sq(x0, y0, x1, y1 float64) []Point {
	return []Point{{X: x0, Y: y0}, {X: x1, Y: y0}, {X: x1, Y: y1}, {X: x0, Y: y1}}
}

func mustPoly(t *testing.T, v []Point) *Polygon {
	p, err := NewPolygon(v)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func mustIntersect(t *testing.T, a, b *Polygon) *Polygon {
	r, err := a.Intersect(b)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func naiveIntersect(a, b []Point) []Point {
	out := a
	for i := range b {
		out = cvex.ClipLeft(out, b[i], b[(i+1)%len(b)])
	}
	if len(out) < 3 || cvex.Area2(out) <= eps {
		return nil
	}
	return out
}

// randConvex 生成随机凸多边形：圆周上随机角度排序后的点必严格凸且逆时针。
func randConvex(r *rand.Rand) []Point {
	n := 3 + r.IntN(8)
	ang := make([]float64, n)
	for i := range ang {
		ang[i] = r.Float64() * 2 * math.Pi
	}
	slices.Sort(ang)
	cx, cy := float64(r.IntN(80)-40), float64(r.IntN(80)-40)
	rad := 10 + r.Float64()*80
	p := make([]Point, n)
	for i, a := range ang {
		p[i] = Point{X: cx + rad*math.Cos(a), Y: cy + rad*math.Sin(a)}
	}
	return p
}

// TestNaiveConsistency 不变量1：Intersect 与朴素逐边裁剪一致（随机凸多边形对）。
func TestNaiveConsistency(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for k := 0; k < 500; k++ {
		a, b := randConvex(r), randConvex(r)
		got := mustIntersect(t, mustPoly(t, a), mustPoly(t, b))
		if want := naiveIntersect(a, b); !samePoly(got.Vertices(), want) {
			t.Fatalf("#%d: %v != 朴素结果 %v", k, got.Vertices(), want)
		}
	}
}

// TestCoverage 不变量2、3：结果严格凸、顶点满足双方全部半平面、严格内部点必被覆盖。
func TestCoverage(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	for k := 0; k < 200; k++ {
		a, b := randConvex(r), randConvex(r)
		g := mustIntersect(t, mustPoly(t, a), mustPoly(t, b)).Vertices()
		for i, v := range g {
			if cvex.Orient(g[i], g[(i+1)%len(g)], g[(i+2)%len(g)]) <= eps {
				t.Fatalf("#%d 结果非严格凸", k)
			}
			if !inside(v.X, v.Y, a, -eps) || !inside(v.X, v.Y, b, -eps) {
				t.Fatalf("#%d 顶点 %v 越出半平面", k, v)
			}
		}
		for s := 0; s < 200; s++ {
			x, y := float64(r.IntN(240)-120), float64(r.IntN(240)-120)
			if inside(x, y, a, eps) && inside(x, y, b, eps) && !inside(x, y, g, -eps) {
				t.Fatalf("#%d 严格内部点 (%v,%v) 未被覆盖", k, x, y)
			}
		}
	}
}

func TestRejections(t *testing.T) {
	cases := []struct {
		name  string
		verts []Point
		want  error
	}{
		{"顶点数不足", []Point{{X: 0, Y: 0}, {X: 1, Y: 1}}, ErrTooFewVertices},
		{"坐标越界", []Point{{X: 0, Y: 0}, {X: 2e4, Y: 0}, {X: 0, Y: 1}}, ErrOutOfRange},
		{"非凸", []Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 2, Y: 1}, {X: 0, Y: 4}}, ErrNotConvex},
		{"顶点重复", []Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 4, Y: 0}, {X: 0, Y: 4}}, ErrNotConvex},
		{"自交", []Point{{X: 0, Y: 0}, {X: 4, Y: 4}, {X: 0, Y: 4}, {X: 4, Y: 0}}, ErrNotConvex},
	}
	for _, tc := range cases {
		if p, err := NewPolygon(tc.verts); !errors.Is(err, tc.want) || p != nil {
			t.Errorf("%s: err=%v p=%v，期望 %v 且无部分结果", tc.name, err, p, tc.want)
		}
	}
}

func TestNoPartialResult(t *testing.T) {
	if p, err := NewPolygon([]Point{{X: 0, Y: 0}, {X: 1, Y: 0}}); p != nil || err == nil {
		t.Fatal("被拒输入产生了部分结果")
	}
	r := mustIntersect(t, mustPoly(t, sq(0, 0, 4, 4)), mustPoly(t, sq(2, 2, 6, 6)))
	if !samePoly(r.Vertices(), sq(2, 2, 4, 4)) {
		t.Fatal("被拒后无法继续正常使用")
	}
}

func TestConcurrentReadOnly(t *testing.T) {
	r := mustIntersect(t, mustPoly(t, sq(0, 0, 4, 4)), mustPoly(t, sq(2, 2, 6, 6)))
	want := r.Vertices()
	start := make(chan struct{})
	var wg sync.WaitGroup
	var bad atomic.Bool
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 50 && !bad.Load(); j++ {
				if got := r.Vertices(); r.Empty() || !slices.Equal(got, want) || SelfCheck() != nil {
					bad.Store(true)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if bad.Load() {
		t.Fatal("并发只读结果不一致")
	}
}
