package crel

import (
	"testing"

	"ontology/cgeom"
)

// gridEngine lays m short disjoint segments on a sparse uniform k*k lattice;
// only the constant-size neighborhood of a corner query can be relevant.
func gridEngine(m int) (*Engine, []Edge) {
	k := 1
	for k*k < m {
		k++
	}
	eds := make([]Edge, 0, m)
	for i := 0; i < m; i++ {
		x, y := (i%k)*256, (i/k)*256
		eds = append(eds, Edge{A: cgeom.Point{X: x, Y: y}, B: cgeom.Point{X: x + 10, Y: y}})
	}
	return NewEngine(nil, eds), eds
}

func naiveMin(eds []Edge, p cgeom.Point) cgeom.Rat2 {
	best := cgeom.PointSegDist2(p, eds[0].A, eds[0].B)
	for i := 1; i < len(eds); i++ {
		if d := cgeom.PointSegDist2(p, eds[i].A, eds[i].B); cgeom.CmpRat2(d, best) < 0 {
			best = d
		}
	}
	return best
}

// TestGridNoFullScan: as m grows 100 -> 10000 over a proportionally larger
// grid, a tiny corner circle must touch only an m-independent constant number
// of edges, proving the grid prunes instead of scanning the whole edge table.
func TestGridNoFullScan(t *testing.T) {
	ms := []int{100, 400, 1000, 2500, 6400, 10000}
	p := cgeom.Point{X: 5, Y: 5}
	const bound int64 = 16
	var counts []int64
	for _, m := range ms {
		e, eds := gridEngine(m)
		got := e.MinDist2(p)
		if cgeom.CmpRat2(got, naiveMin(eds, p)) != 0 {
			t.Fatalf("m=%d: grid D %v != naive D %v", m, got, naiveMin(eds, p))
		}
		n := e.checked.Load()
		counts = append(counts, n)
		if n > bound {
			t.Fatalf("m=%d: checked %d edges, want <= %d (constant)", m, n, bound)
		}
	}
	for i := 1; i < len(counts); i++ { // no growth with m at all
		if counts[i] > counts[0] {
			t.Fatalf("checked edges grew with m: %v", counts)
		}
	}
}

// TestRingsFindDistantEdge: when the corner neighborhood is empty, ring
// expansion must keep going until the unique far edge is found exactly.
func TestRingsFindDistantEdge(t *testing.T) {
	eds := []Edge{
		{A: cgeom.Point{X: 0, Y: 0}, B: cgeom.Point{X: 10, Y: 0}},
		{A: cgeom.Point{X: 5000, Y: 5000}, B: cgeom.Point{X: 5010, Y: 5000}},
	}
	e := NewEngine(nil, eds)
	for _, tc := range []struct {
		p cgeom.Point
		d int64
	}{
		{cgeom.Point{X: 5, Y: 5}, 25},
		{cgeom.Point{X: 5005, Y: 5005}, 25},
	} {
		if d := e.MinDist2(tc.p); cgeom.CmpRat2Int(d, tc.d) != 0 {
			t.Fatalf("p=%v: got D %v, want %d", tc.p, d, tc.d)
		}
	}
}
