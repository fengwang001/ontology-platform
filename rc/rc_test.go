package rc

import (
	"testing"

	"ontology/cal"
)

// TestCounterLinear 钉住复杂度：一次 solve 实际求 d² 的顶点对个数不超过 3m，
// 随 m 线性增长，而不是 O(m²) 两两枚举。计数器 cnt 是非导出字段，只有
// 包内测试能读到它。
func TestCounterLinear(t *testing.T) {
	for _, m := range []int{100, 300, 1000, 3000, 10000} {
		s := &solver{poly: regular(m)}
		s.solve()
		if s.cnt > 3*m {
			t.Fatalf("m=%d: counter %d exceeds 3m=%d (super-linear)", m, s.cnt, 3*m)
		}
		if s.cnt < m { //  sanity：至少每条边都求过值
			t.Fatalf("m=%d: counter %d suspiciously small", m, s.cnt)
		}
	}
}

// TestDiameterKnown 钉住两个手算用例（NOTES.md 第三节）。
func TestDiameterKnown(t *testing.T) {
	cases := []struct {
		name  string
		poly  [][2]int64
		want  int64
		pairs [][2]int
	}{
		{"pentagon", [][2]int64{{0, 0}, {5, 1}, {6, 4}, {3, 6}, {1, 5}}, 52, [][2]int{{0, 2}}},
		{"rectangle", [][2]int64{{0, 0}, {6, 0}, {6, 4}, {0, 4}}, 52, [][2]int{{0, 2}, {1, 3}}},
	}
	for _, c := range cases {
		poly := make([]cal.Point, len(c.poly))
		for i, p := range c.poly {
			poly[i] = cal.Point{X: p[0], Y: p[1]}
		}
		d2, pairs := Diameter(poly)
		if d2 != c.want || !eqPairs(pairs, c.pairs) {
			t.Fatalf("%s: got d2=%d pairs=%v, want %d %v", c.name, d2, pairs, c.want, c.pairs)
		}
	}
}

func eqPairs(a, b [][2]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
