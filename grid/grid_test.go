package grid

import (
	"math"
	"testing"

	"ontology/geom"
)

func TestInsertReject(t *testing.T) {
	g := New(geom.Rect{X0: 0, Y0: 0, X1: 10, Y1: 10}, 4)
	cases := []struct {
		name string
		p    geom.Point
		want bool
	}{
		{"正常点", geom.Point{5, 5}, true},
		{"零点", geom.Point{0, 0}, true},
		{"负零", geom.Point{math.Copysign(0, -1), 0}, true},
		{"NaN", geom.Point{math.NaN(), 1}, false},
		{"+Inf", geom.Point{math.Inf(1), 1}, false},
		{"-Inf", geom.Point{1, math.Inf(-1)}, false},
		{"越界", geom.Point{10, 5}, false},
	}
	var wantSkipped uint64
	for _, c := range cases {
		if got := g.Insert(c.p); got != c.want {
			t.Errorf("%s: Insert=%v want %v", c.name, got, c.want)
		}
		if !c.want {
			wantSkipped++
		}
	}
	if got := g.Skipped(); got != wantSkipped {
		t.Errorf("Skipped=%d want %d", got, wantSkipped)
	}
	if got := g.Total(); got != 3 {
		t.Errorf("Total=%d want 3", got)
	}
}

func TestDeleteAndContains(t *testing.T) {
	g := New(geom.Rect{X0: 0, Y0: 0, X1: 10, Y1: 10}, 2)
	pts := []geom.Point{{1, 1}, {2, 2}, {3, 3}, {4, 4}}
	for _, p := range pts {
		g.Insert(p)
	}
	cases := []struct {
		name   string
		p      geom.Point
		delOK  bool
		inGrid bool
	}{
		{"删除存在点", geom.Point{2, 2}, true, false},
		{"删除不存在点", geom.Point{9, 9}, false, false},
		{"未动的点仍在", geom.Point{3, 3}, true, false},
	}
	for _, c := range cases {
		if got := g.Delete(c.p); got != c.delOK {
			t.Errorf("%s: Delete=%v want %v", c.name, got, c.delOK)
		}
		if got := g.Contains(c.p); got != c.inGrid {
			t.Errorf("%s: Contains=%v want %v", c.name, got, c.inGrid)
		}
	}
	if got := g.Total(); got != 2 {
		t.Errorf("Total=%d want 2", got)
	}
}
