package un

import (
	"math"
	"testing"

	"ontology/poly"
)

// circle 生成圆内接 m 边形（逆时针凸多边形）。
func circle(cx, cy, r float64, m int) []poly.Point {
	p := make([]poly.Point, m)
	for i := range p {
		a := 2 * math.Pi * float64(i) / float64(m)
		p[i] = poly.Point{X: cx + r*math.Cos(a), Y: cy + r*math.Sin(a)}
	}
	return p
}

// TestProbeCountLinear 多档 m 下断言：实际边-边求交判定次数不随 m² 增长，
// 而是被网格剪枝压到 c·m（c 为与 m 无关的小常数）以内。
func TestProbeCountLinear(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		A, B := circle(0, 0, 9000, m), circle(10000, 0, 9000, m)
		if _, err := Union(A, B); err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		if got, lim := probes.Load(), int64(4*m); got > lim {
			t.Fatalf("m=%d: 求交判定 %d 次 > %d（应不随 m² 增长）", m, got, lim)
		}
	}
}
