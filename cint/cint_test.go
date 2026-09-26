package cint

import (
	"testing"

	"ontology/cvex"
)

func sq(x0, y0, x1, y1 float64) []cvex.Point {
	return []cvex.Point{{X: x0, Y: y0}, {X: x1, Y: y0}, {X: x1, Y: y1}, {X: x0, Y: y1}}
}

// TestClipCounter 钉住复杂度约束：包围盒不相交时 O(1) 判空，裁剪次数为 0。
// 计数器 clips 是非导出字段，此处（包内测试）直接读取，不经任何导出接口。
func TestClipCounter(t *testing.T) {
	a := sq(0, 0, 4, 4)
	for _, m := range []int{100, 1000, 10000} {
		var c Clipper
		for i := 0; i < m; i++ {
			d := float64(100 + i*10)
			if got := c.Intersect(a, sq(d, d, d+1, d+1)); got != nil {
				t.Fatalf("m=%d i=%d: 包围盒不相交却得到 %v", m, i, got)
			}
			if c.clips != 0 {
				t.Fatalf("m=%d i=%d: 包围盒不相交却执行了 %d 次裁剪", m, i, c.clips)
			}
		}
	}
}

// TestClipCounterCounts 验证计数器记录的是实际执行的半平面裁剪次数。
func TestClipCounterCounts(t *testing.T) {
	a := sq(0, 0, 4, 4)
	cases := []struct {
		name string
		b    []cvex.Point
		want int
	}{
		{"相交正方形用满4条边", sq(2, 2, 6, 6), 4},
		{"共边退化也用满4条边", sq(4, 0, 8, 4), 4},
		{"首边裁空提前结束", []cvex.Point{{X: 3, Y: 5.5}, {X: 5.5, Y: 3}, {X: 6, Y: 6}}, 1},
	}
	for _, tc := range cases {
		var c Clipper
		c.Intersect(a, tc.b)
		if c.clips != tc.want {
			t.Errorf("%s: clips=%d 期望 %d", tc.name, c.clips, tc.want)
		}
	}
}
