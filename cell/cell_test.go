package cell

import (
	"testing"

	"ontology/geom"
)

// 分裂线上的点归属唯一子格：x 中线进右，y 中线进上。
func TestSplitLineOwnership(t *testing.T) {
	cases := []struct {
		name string
		p    geom.Point
		want int
	}{
		{"分裂线x归右", geom.Point{2, 1}, SE},
		{"分裂线y归上", geom.Point{1, 2}, NW},
		{"分裂线交点归右上", geom.Point{2, 2}, NE},
		{"普通左下", geom.Point{1, 1}, SW},
	}
	for _, c := range cases {
		root := New(geom.Rect{X0: 0, Y0: 0, X1: 4, Y1: 4}, 1)
		root.Insert(geom.Point{0.5, 0.5}, 0)
		root.Insert(c.p, 0)
		if !root.Divided {
			t.Fatalf("%s: 未分裂", c.name)
		}
		for i, ch := range root.Children {
			has := ch.Total() > 0 && ch.Contains(c.p)
			if want := i == c.want; has != want {
				t.Errorf("%s: 子格%d 含点=%v want %v", c.name, i, has, want)
			}
		}
	}
}

// 分裂前后点总数守恒且每个点仍可查找。
func TestSplitConservesPoints(t *testing.T) {
	root := New(geom.Rect{X0: 0, Y0: 0, X1: 8, Y1: 8}, 4)
	pts := []geom.Point{{1, 1}, {7, 7}, {4, 4}, {2, 6}, {6, 2}, {3, 3}, {5, 5}}
	for _, p := range pts {
		root.Insert(p, 0)
	}
	if got := root.Total(); got != len(pts) {
		t.Fatalf("Total=%d want %d", got, len(pts))
	}
	for _, p := range pts {
		if !root.Contains(p) {
			t.Errorf("分裂后丢点 %v", p)
		}
	}
}

// 1000 个完全相同坐标的点：不栈溢出、深度受限、全部可查。
func TestIdenticalPointsTerminate(t *testing.T) {
	root := New(geom.Rect{X0: 0, Y0: 0, X1: 1024, Y1: 1024}, 4)
	p := geom.Point{2.5, 2.5}
	for i := 0; i < 1000; i++ {
		root.Insert(p, 0)
	}
	if got := root.Total(); got != 1000 {
		t.Fatalf("Total=%d want 1000", got)
	}
	if d := root.Depth(); d > MaxDepth {
		t.Fatalf("Depth=%d 超过上限 %d", d, MaxDepth)
	}
	if !root.Contains(p) {
		t.Fatal("同坐标点丢失")
	}
}

func TestDelete(t *testing.T) {
	cases := []struct {
		name string
		p    geom.Point
		want bool
	}{
		{"存在的点", geom.Point{1, 1}, true},
		{"不存在的点", geom.Point{9, 9}, false},
	}
	for _, c := range cases {
		root := New(geom.Rect{X0: 0, Y0: 0, X1: 8, Y1: 8}, 2)
		root.Insert(geom.Point{1, 1}, 0)
		root.Insert(geom.Point{2, 2}, 0)
		if got := root.Delete(c.p); got != c.want {
			t.Errorf("%s: Delete=%v want %v", c.name, got, c.want)
		}
	}
	root := New(geom.Rect{X0: 0, Y0: 0, X1: 8, Y1: 8}, 2)
	root.Insert(geom.Point{1, 1}, 0)
	root.Delete(geom.Point{1, 1})
	if root.Contains(geom.Point{1, 1}) || root.Total() != 0 {
		t.Error("删除后仍可查到或总数未减")
	}
}
