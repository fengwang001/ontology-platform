package mec

import (
	"testing"

	"ontology/circ"
)

func pt(x, y int) circ.Point { return circ.Point{X: x, Y: y} }

// brute 暴力枚举：单点、点对直径、三点圆（含回退），取覆盖全部点的最小者。
func brute(pts []circ.Point) circ.Circle {
	var best circ.Circle
	have := false
	try := func(c circ.Circle) {
		for _, p := range pts {
			if !c.Contains(p) {
				return
			}
		}
		if !have || c.R2.Cmp(best.R2) < 0 {
			best, have = c, true
		}
	}
	for i, a := range pts {
		try(circ.FromPoint(a))
		for j := i + 1; j < len(pts); j++ {
			try(circ.FromDiameter(a, pts[j]))
			for k := j + 1; k < len(pts); k++ {
				try(circ.FromThree(a, pts[j], pts[k]))
			}
		}
	}
	return best
}

func sameCircle(a, b circ.Circle) bool {
	return a.Cx.Cmp(b.Cx) == 0 && a.Cy.Cmp(b.Cy) == 0 && a.R2.Cmp(b.R2) == 0
}

// 不变量 1、2：逐次插入后 MEC 都与暴力枚举的最小覆盖圆一致。
func TestMatchesBruteForce(t *testing.T) {
	sets := [][]circ.Point{
		{pt(0, 0)}, {pt(0, 0), pt(6, 0)},
		{pt(0, 0), pt(6, 0), pt(0, 8)}, {pt(0, 0), pt(6, 0), pt(1, 1)},
		{pt(0, 0), pt(3, 0), pt(6, 0)},
		{pt(-3, 7), pt(5, -2), pt(0, 0), pt(4, 4), pt(-6, -6)},
		{pt(2, 2), pt(-2, 2), pt(2, -2), pt(-2, -2), pt(0, 3), pt(3, 0)},
	}
	// 多档规模随机点集，固定种子可复现。
	seed := int64(1)
	for n := 1; n <= 12; n++ {
		for rep := 0; rep < 20; rep++ {
			seed = seed*6364136223846793005 + 1442695040888963407
			r := seed
			seen := map[circ.Point]bool{}
			var pts []circ.Point
			for len(pts) < n {
				r = r*2862933555777941757 + 3037000493
				q := pt(int(r%61)-30, int(r/61%61)-30)
				if !seen[q] {
					seen[q] = true
					pts = append(pts, q)
				}
			}
			sets = append(sets, pts)
		}
	}
	for i, pts := range sets {
		w := New()
		for _, p := range pts {
			w.Insert(p)
		}
		got, ok := w.Circle()
		if !ok || !sameCircle(got, brute(pts)) {
			t.Errorf("set %d %v: got %v,%v,%v", i, pts, got.Cx, got.Cy, got.R2)
		}
	}
}

// 不变量（复杂度）：落在当前圆内的插入只做 1 次「点在圆内」判定，不重新扫描。
func TestInteriorInsertionSingleCheck(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		w := New()
		w.Insert(pt(0, 0))
		w.Insert(pt(1000, 0))
		for i := 0; i < m; i++ {
			w.Insert(pt(450+i%100, i/100-50)) // 严格在直径圆内
			if w.checks != 1 {
				t.Fatalf("m=%d i=%d: checks=%d, want 1", m, i, w.checks)
			}
		}
	}
}

// 不变量 3：所有点落在圆内或圆上，边界点恰在圆上且为 1..3 个。
func TestCoverageAndBoundary(t *testing.T) {
	seed := int64(99)
	for trial := 0; trial < 60; trial++ {
		seen := map[circ.Point]bool{}
		var pts []circ.Point
		for n := 1 + trial%10; len(pts) < n; {
			seed = seed*2862933555777941757 + 3037000493
			if q := pt(int(seed%61)-30, int(seed/61%61)-30); !seen[q] {
				seen[q] = true
				pts = append(pts, q)
			}
		}
		w := New()
		for _, p := range pts {
			w.Insert(p)
		}
		c, _ := w.Circle()
		for _, p := range pts {
			if !c.Contains(p) {
				t.Fatalf("trial %d: %v not covered", trial, p)
			}
		}
		if len(c.B) < 1 || len(c.B) > 3 {
			t.Fatalf("trial %d: %d boundary points", trial, len(c.B))
		}
		for _, p := range c.B {
			if !c.OnBoundary(p) {
				t.Fatalf("trial %d: boundary %v not on circle", trial, p)
			}
		}
	}
}

// 导出入口 InteriorProbe 只给布尔结论，供 demo 使用。
func TestInteriorProbe(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		if !InteriorProbe(m) {
			t.Errorf("InteriorProbe(%d) = false", m)
		}
	}
}
