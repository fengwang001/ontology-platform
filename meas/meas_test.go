package meas

import (
	"math"
	"sync/atomic"
	"testing"

	"ontology/poly"
)

// ngon 生成半径 r 圆上的 m 个整数点（逆时针简单多边形）。
func ngon(m, r int) []poly.Point {
	v := make([]poly.Point, m)
	for i := range v {
		t := 2 * math.Pi * float64(i) / float64(m)
		v[i] = poly.Point{X: int64(math.Round(float64(r) * math.Cos(t))),
			Y: int64(math.Round(float64(r) * math.Sin(t)))}
	}
	return v
}

// TestReadsEqualN 钉住复杂度不变量：一次 Area/Centroid 实际读取的顶点数
// 恰好等于 n（每条边读一次、每顶点 O(1)），证明 O(n) 线性、无重复遍历。
// 计数器是非导出字段，本测试与实现同包，直接读它，不经任何导出接口。
func TestReadsEqualN(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		p := NewPolygon(ngon(m, 9000))
		_ = p.Area()
		if got := atomic.LoadInt64(&p.reads); got != int64(m) {
			t.Fatalf("Area: m=%d 计数=%d， want %d", m, got, m)
		}
		_ = p.Centroid()
		if got := atomic.LoadInt64(&p.reads); got != int64(m) {
			t.Fatalf("Centroid: m=%d 计数=%d， want %d", m, got, m)
		}
	}
}

// TestMeasureLShape 钉住第三节推导：L 形面积 20、重心 (11/5,11/5)。
func TestMeasureLShape(t *testing.T) {
	p := NewPolygon([]poly.Point{{X: 0, Y: 0}, {X: 6, Y: 0}, {X: 6, Y: 2},
		{X: 2, Y: 2}, {X: 2, Y: 6}, {X: 0, Y: 6}})
	if a := p.Area(); a != NewRat(20, 1) {
		t.Fatalf("面积=%v，want 20/1", a)
	}
	if c := p.Centroid(); c.X != NewRat(11, 5) || c.Y != NewRat(11, 5) {
		t.Fatalf("重心=%v，want (11/5,11/5)", c)
	}
}

// TestRatNormalize 表驱动：既约与符号归一。
func TestRatNormalize(t *testing.T) {
	cases := []struct{ num, den int64 }{{2, 4}, {-2, 4}, {2, -4}, {0, 7}, {6, 3}}
	want := []Rat{{1, 2}, {-1, 2}, {-1, 2}, {0, 1}, {2, 1}}
	for i, c := range cases {
		if got := NewRat(c.num, c.den); got != want[i] {
			t.Fatalf("NewRat(%d,%d)=%v，want %v", c.num, c.den, got, want[i])
		}
	}
}
