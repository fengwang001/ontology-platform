package rc

import (
	"math"
	"testing"

	"ontology/cal"
)

// 生成近似正 m 边形的凸多边形（逆时针、整数坐标；rc 不校验坐标范围）。
func regularish(m int, r float64) []cal.Point {
	p := make([]cal.Point, m)
	for i := range p {
		t := 2 * math.Pi * float64(i) / float64(m)
		p[i] = cal.Point{X: int64(math.Round(r * math.Cos(t))), Y: int64(math.Round(r * math.Sin(t)))}
	}
	return p
}

// 钉住复杂度：一次 Diameter 实际求 d² 的顶点对个数必须随 m 线性（=2m），
// 而不是暴力枚举的 m(m-1)/2。计数器为非导出字段，仅本包测试可读。
func TestCounterLinearInM(t *testing.T) {
	for _, m := range []int{100, 300, 1000, 3000, 10000} {
		c := New(regularish(m, float64(m)*100))
		c.Diameter()
		got := c.count.Load()
		if got != int64(2*m) {
			t.Fatalf("m=%d: d² 求值计数 %d，应为 2m=%d（O(n) 对跖点对，非 O(n²) 枚举）", m, got, 2*m)
		}
		if brute := int64(m) * int64(m-1) / 2; got >= brute {
			t.Fatalf("m=%d: 计数 %d 未显著低于暴力枚举 %d", m, got, brute)
		}
	}
}
