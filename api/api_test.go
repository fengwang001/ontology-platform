package api

import (
	"errors"
	"math"
	"ontology/poly"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func circle(cx, cy, r float64, n int) []poly.Point {
	p := make([]poly.Point, n)
	for i := range p {
		a := 2 * math.Pi * float64(i) / float64(n)
		p[i] = poly.Point{X: math.Round(cx + r*math.Cos(a)), Y: math.Round(cy + r*math.Sin(a))}
	}
	return p
}

func rect(x0, y0, x1, y1 float64) []poly.Point {
	return []poly.Point{{X: x0, Y: y0}, {X: x1, Y: y0}, {X: x1, Y: y1}, {X: x0, Y: y1}}
}

func must(t *testing.T, v []poly.Point) *Polygon {
	p, err := NewPolygon(v)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// pairs 循环生成多档规模的相交多边形对（n 档 × 偏移档）。
func pairs(t *testing.T) [][2]*Polygon {
	var out [][2]*Polygon
	for _, n := range []int{3, 5, 8, 20, 50} {
		for _, dx := range []float64{500, 2000} {
			out = append(out, [2]*Polygon{must(t, circle(0, 0, 9000, n)), must(t, circle(dx, 1000, 8000, n+1))})
		}
	}
	return out
}

// TestInvariantArea 不变量1：并集面积 == area(A)+area(B)−area(A∩B)（对照 rectInter）。
func TestInvariantArea(t *testing.T) {
	tab := [][2][]poly.Point{
		{rect(0, 0, 3, 3), rect(2, 2, 5, 5)},
		{rect(0, 0, 6, 6), rect(1, 1, 2, 2)},
		{rect(1, 1, 9, 5), rect(0, 3, 8, 8)},
	}
	for _, pr := range tab {
		U, err := must(t, pr[0]).Union(must(t, pr[1]))
		if err != nil {
			t.Fatal(err)
		}
		want := poly.Area2(pr[0])/2 + poly.Area2(pr[1])/2 - rectInter(pr[0], pr[1])
		if got := poly.Area2(U.Vertices()) / 2; got != want {
			t.Fatalf("面积不守恒: got %v, want %v", got, want)
		}
	}
}

// TestInvariantShape 不变量2：结果自洽（无自交、无重复、逆时针、面积非负）。
func TestInvariantShape(t *testing.T) {
	for _, pr := range pairs(t) {
		U, err := pr[0].Union(pr[1])
		if err != nil {
			t.Fatal(err)
		}
		if err := U.SelfCheck(); err != nil {
			t.Fatalf("结果不自洽: %v", err)
		}
	}
}

// TestInvariantCoverage 不变量2b/3：顶点归属（边界上或严格在内）+ 采样点双向覆盖。
func TestInvariantCoverage(t *testing.T) {
	for _, pr := range pairs(t) {
		U, err := pr[0].Union(pr[1])
		if err != nil {
			t.Fatal(err)
		}
		if !pointsOK(U.Vertices(), pr[0].Vertices(), pr[1].Vertices()) {
			t.Fatal("顶点归属或覆盖不正确")
		}
	}
}

// TestInvariantAtomicFailure 不变量4：四类故障各有可判定错误、互不相同、不留部分结果。
func TestInvariantAtomicFailure(t *testing.T) {
	tab := []struct {
		name  string
		verts []poly.Point
		want  error
	}{
		{"顶点数不足", []poly.Point{{X: 0, Y: 0}, {X: 1, Y: 0}}, ErrTooFewVertices},
		{"自交", []poly.Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 1, Y: 2}, {X: 3, Y: 3}}, ErrSelfIntersect},
		{"相邻重复", []poly.Point{{X: 0, Y: 0}, {X: 0, Y: 0}, {X: 1, Y: 1}}, ErrDuplicate},
		{"顺时针", []poly.Point{{X: 0, Y: 0}, {X: 0, Y: 1}, {X: 1, Y: 0}}, ErrClockwise},
		{"坐标越界", []poly.Point{{X: 0, Y: 0}, {X: 20000, Y: 0}, {X: 0, Y: 1}}, ErrOutOfRange},
	}
	seen := map[error]bool{}
	for _, c := range tab {
		p, err := NewPolygon(c.verts)
		if !errors.Is(err, c.want) || p != nil {
			t.Fatalf("%s: got (%v, %v), want %v", c.name, p, err, c.want)
		}
		seen[c.want] = true
	}
	if len(seen) != len(tab) {
		t.Fatal("四类故障的错误不互不相同")
	}
	_ = must(t, circle(0, 0, 100, 8)) // 被拒后仍可正常使用
}

// TestConcurrentReads 并发只读同一并集结果，顶点序列必须逐字段相同（不用 sleep）。
func TestConcurrentReads(t *testing.T) {
	U, err := must(t, circle(0, 0, 9000, 8)).Union(must(t, circle(1000, 1000, 8000, 9)))
	if err != nil {
		t.Fatal(err)
	}
	want := U.Vertices()
	start := make(chan struct{})
	var wg sync.WaitGroup
	var bad atomic.Bool
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for k := 0; k < 50; k++ {
				if U.SelfCheck() != nil || !reflect.DeepEqual(U.Vertices(), want) {
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

func TestSelfTest(t *testing.T) {
	if err := SelfTest(); err != nil {
		t.Fatal(err)
	}
}
