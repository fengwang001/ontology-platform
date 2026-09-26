package tri

import (
	"ontology/geo"
	"reflect"
	"sort"
	"testing"
)

func P(x, y int) geo.Point             { return geo.Point{X: x, Y: y} }
func T(a, b, c geo.Point) [3]geo.Point { return [3]geo.Point{a, b, c} }
func sortTris(ts [][3]geo.Point) {
	sort.Slice(ts, func(i, j int) bool {
		for k := 0; k < 3; k++ {
			if a, b := ts[i][k], ts[j][k]; a != b {
				return a.X < b.X || a.X == b.X && a.Y < b.Y
			}
		}
		return false
	})
}
func gridMesh(side, seed0 int) (*Mesh, []geo.Point) {
	pts := make([]geo.Point, 0, side*side)
	for i := 0; i < side*side; i++ {
		pts = append(pts, P(i%side*99, i/side*99))
	}
	seed := uint64(seed0) // LCG Fisher-Yates: random insertion order
	for i := len(pts) - 1; i > 0; i-- {
		seed = seed*6364136223846793005 + 1442695040888963407
		j := int(seed>>33) % (i + 1)
		pts[i], pts[j] = pts[j], pts[i]
	}
	b, in := New(), []geo.Point{}
	for _, p := range pts {
		if b.Insert(p) == nil {
			in = append(in, p)
		}
	}
	return b, in
}
func TestSixStepSequence(t *testing.T) {
	want := [][][3]geo.Point{
		nil, nil,
		{T(P(0, 4), P(0, 0), P(4, 0))},
		{T(P(0, 4), P(0, 0), P(4, 0)), T(P(4, 4), P(0, 4), P(4, 0))},
		{T(P(3, 3), P(0, 0), P(4, 0)), T(P(3, 3), P(0, 4), P(0, 0)),
			T(P(3, 3), P(4, 0), P(4, 4)), T(P(3, 3), P(4, 4), P(0, 4))},
		{T(P(2, 1), P(0, 0), P(4, 0)), T(P(2, 1), P(0, 4), P(0, 0)),
			T(P(2, 1), P(3, 3), P(0, 4)), T(P(2, 1), P(4, 0), P(3, 3)),
			T(P(3, 3), P(4, 0), P(4, 4)), T(P(3, 3), P(4, 4), P(0, 4))},
	}
	b := New()
	for i, p := range sixPts { // one cumulative build; snapshot after each insertion
		if err := b.Insert(p); err != nil {
			t.Fatalf("step %d: %v", i+1, err)
		}
		got := b.Triangles()
		sortTris(got)
		sortTris(want[i])
		if !reflect.DeepEqual(got, want[i]) {
			t.Fatalf("step %d = %v, want %v", i+1, got, want[i])
		}
	}
}
func validate(t *testing.T, b *Mesh, in []geo.Point) {
	t.Helper()
	tris, ec := b.Triangles(), map[[2]geo.Point]int{}
	for _, f := range tris {
		if geo.Orient2D(f[0], f[1], f[2]) <= 0 {
			t.Fatal("non-CCW/degenerate face")
		}
		for _, q := range in {
			if q != f[0] && q != f[1] && q != f[2] && geo.InCircle(f[0], f[1], f[2], q) > 0 {
				t.Fatalf("circumcircle of %v contains %v", f, q)
			}
		}
		for k := 0; k < 3; k++ {
			a, c := f[(k+1)%3], f[(k+2)%3]
			if c.X < a.X || c.X == a.X && c.Y < a.Y {
				a, c = c, a
			}
			ec[[2]geo.Point{a, c}]++
		}
	}
	h := 0
	for _, n := range ec {
		if n > 2 {
			t.Fatal("edge shared by >2 faces")
		}
		if n == 1 {
			h++
		}
	}
	if len(in) >= 3 && len(tris) != 2*len(in)-2-h {
		t.Fatalf("faces=%d want %d", len(tris), 2*len(in)-2-h)
	}
}
func TestRandomTriangulation(t *testing.T) {
	for _, c := range []struct{ side, seed int }{{4, 1}, {4, 7}, {7, 3}, {7, 9}, {12, 5}} {
		b, in := gridMesh(c.side, c.seed)
		validate(t, b, in)
	}
}

var sixPts = []geo.Point{P(0, 0), P(4, 0), P(0, 4), P(4, 4), P(3, 3), P(2, 1)}

func TestEdgeIncidence(t *testing.T) {
	b, in := gridMesh(3, 1) // every interior edge has exactly two incidences
	validate(t, b, in)
}
func TestPointInTriangle(t *testing.T) { // strict interior: edge/vertex/outside false
	a, b, c := P(0, 0), P(6, 0), P(0, 6)
	in, edge, vtx, hyp, out := geo.PointInTriangle(P(1, 1), a, b, c), geo.PointInTriangle(P(0, 3), a, b, c), geo.PointInTriangle(a, a, b, c), geo.PointInTriangle(P(3, 3), a, b, c), geo.PointInTriangle(P(-1, 1), a, b, c)
	if !in || edge || vtx || hyp || out {
		t.Fatalf("in=%v edge=%v vtx=%v hyp=%v out=%v", in, edge, vtx, hyp, out)
	}
}
func TestPredicateCountBounded(t *testing.T) {
	for _, side := range []int{10, 30, 50, 70, 100} {
		b, _ := gridMesh(side, 42)
		if err := b.Insert(P(50, 50)); err != nil || b.nc > 40 {
			t.Fatalf("m=%d predicates=%d not bounded by 40", side*side, b.nc)
		}
	}
}
func TestRejectedInsertLeavesState(t *testing.T) {
	b := New()
	for _, p := range []geo.Point{P(0, 0), P(4, 0), P(0, 4), P(4, 4)} {
		if err := b.Insert(p); err != nil {
			t.Fatal(err)
		}
	}
	before := b.Triangles()
	if err := b.Insert(P(4, 0)); err != ErrDuplicatePoint {
		t.Fatalf("duplicate: %v", err)
	}
	// a refused insert mutates nothing, so face order is byte-identical
	if !reflect.DeepEqual(before, b.Triangles()) {
		t.Fatal("duplicate insert changed state")
	}
	if b.Insert(P(1, 3)) != nil {
		t.Fatal("mesh unusable after refused duplicate")
	}
	c := New()
	if c.Insert(P(0, 0)) != nil || c.Insert(P(1, 1)) != nil ||
		c.Insert(P(2, 2)) != ErrCollinearStart || len(c.Triangles()) != 0 ||
		c.Insert(P(0, 1)) != nil {
		t.Fatal("collinear-start handling failed")
	}
}
