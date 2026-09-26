// Package api is the public entry point for circle–polygon relation checks.
package api

import (
	"errors"
	"fmt"

	"ontology/cgeom"
	"ontology/crel"
)

// Point is an integer coordinate, |X|,|Y| <= 1e4.
type Point = cgeom.Point

// Pairwise-distinct sentinels; every rejection is errors.Is-decidable.
var ErrInvalidPolygon = errors.New("invalid polygon")       // <3 verts, duplicate, self-cross, non-CCW
var ErrInvalidRadius = errors.New("invalid radius")         // r < 0
var ErrOutOfBounds = errors.New("coordinate out of bounds") // vertex or circle center

const maxCoord = 10000

// Polygon is an immutable, concurrency-safe validated polygon.
type Polygon struct{ eng *crel.Engine }

func inBounds(p Point) bool {
	return -maxCoord <= p.X && p.X <= maxCoord && -maxCoord <= p.Y && p.Y <= maxCoord
}
func cross(o, a, b Point) int64 { return int64(a.X-o.X)*int64(b.Y-o.Y) - int64(a.Y-o.Y)*int64(b.X-o.X) }
func onSeg(p, a, b Point) bool {
	return cross(a, b, p) == 0 && min(a.X, b.X) <= p.X && p.X <= max(a.X, b.X) && min(a.Y, b.Y) <= p.Y && p.Y <= max(a.Y, b.Y)
}

// segIntersect: any contact between non-adjacent edges makes it non-simple.
func segIntersect(a, b, c, d Point) bool {
	return cross(a, b, c)*cross(a, b, d) < 0 && cross(c, d, a)*cross(c, d, b) < 0 ||
		onSeg(c, a, b) || onSeg(d, a, b) || onSeg(a, c, d) || onSeg(b, c, d)
}

func validate(poly []Point) error {
	n := len(poly)
	if n < 3 {
		return fmt.Errorf("%w: need >=3 vertices", ErrInvalidPolygon)
	}
	seen := make(map[Point]bool, n)
	var area int64
	for i, p := range poly {
		if !inBounds(p) {
			return fmt.Errorf("%w: vertex %v", ErrOutOfBounds, p)
		}
		if seen[p] {
			return fmt.Errorf("%w: duplicate %v", ErrInvalidPolygon, p)
		}
		seen[p] = true
		q := poly[(i+1)%n]
		area += int64(p.X)*int64(q.Y) - int64(q.X)*int64(p.Y)
	}
	if area <= 0 { // degenerate (0) or clockwise (<0)
		return fmt.Errorf("%w: must be CCW", ErrInvalidPolygon)
	}
	for i := 0; i < n; i++ {
		a, b := poly[i], poly[(i+1)%n]
		for j := i + 1; j < n; j++ {
			if j != i+1 && !(i == 0 && j == n-1) && segIntersect(a, b, poly[j], poly[(j+1)%n]) {
				return fmt.Errorf("%w: self-intersection", ErrInvalidPolygon)
			}
		}
	}
	return nil
}

// New validates everything first, so rejection leaves no partial object.
func New(poly []Point) (*Polygon, error) {
	if err := validate(poly); err != nil {
		return nil, err
	}
	cp := append([]Point(nil), poly...)
	eds := make([]crel.Edge, len(cp))
	for i := range cp {
		eds[i] = crel.Edge{A: cp[i], B: cp[(i+1)%len(cp)]}
	}
	return &Polygon{eng: crel.NewEngine(cp, eds)}, nil
}

// Relation classifies one circle against the validated polygon.
func (p *Polygon) Relation(cx, cy, r int) (crel.Relation, error) {
	if r < 0 {
		return 0, fmt.Errorf("%w: r=%d", ErrInvalidRadius, r)
	}
	c := Point{X: cx, Y: cy}
	if !inBounds(c) {
		return 0, fmt.Errorf("%w: center %v", ErrOutOfBounds, c)
	}
	return p.eng.Classify(c, int64(r)*int64(r)), nil
}

// naive is the O(n) reference: per-edge clamped squared distance, ray cast, four-state rule.
func naive(poly []Point, c Point, r2 int64) crel.Relation {
	best := cgeom.PointSegDist2(c, poly[0], poly[1])
	for i, n := 1, len(poly); i < n; i++ {
		if d := cgeom.PointSegDist2(c, poly[i], poly[(i+1)%n]); cgeom.CmpRat2(d, best) < 0 {
			best = d
		}
	}
	cmp := cgeom.CmpRat2Int(best, r2)
	if cmp == 0 {
		return crel.Tangent
	}
	if cmp < 0 {
		return crel.Crossing
	}
	if cgeom.PointInPoly(c, poly) {
		return crel.Contained
	}
	return crel.Disjoint
}

// SelfCheck verifies exact clamped D, naive agreement, four states, and three distinguishable non-destructive rejections.
func (p *Polygon) SelfCheck() error {
	sq := []Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 4, Y: 4}, {X: 0, Y: 4}}
	q, _ := New(sq) // sq is a constant valid CCW square
	cs := [][5]int{{2, 2, 1, 4, int(crel.Contained)}, {2, 2, 3, 4, int(crel.Crossing)},
		{6, 2, 1, 4, int(crel.Disjoint)}, {5, 2, 1, 1, int(crel.Tangent)},
		{2, 5, 1, 1, int(crel.Tangent)}, {5, -2, 2, 5, int(crel.Disjoint)}}
	states := map[crel.Relation]bool{}
	for _, c := range cs {
		pt, r2 := Point{X: c[0], Y: c[1]}, int64(c[2])*int64(c[2])
		got, e := q.Relation(c[0], c[1], c[2])
		if e != nil || cgeom.CmpRat2Int(q.eng.MinDist2(pt), int64(c[3])) != 0 ||
			got != crel.Relation(c[4]) || got != naive(sq, pt, r2) {
			return errors.New("selfcheck: six-circle mismatch")
		}
		states[got] = true
	}
	if len(states) != 4 {
		return errors.New("selfcheck: four states not covered")
	}
	_, e1 := New(sq[:2])
	_, e2 := New([]Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 0, Y: maxCoord + 1}})
	_, e3 := q.Relation(2, 2, -1)
	got, _ := q.Relation(2, 2, 1)
	names := []string{"polygon", "bounds", "radius", "reusable"}
	oks := []bool{errors.Is(e1, ErrInvalidPolygon), errors.Is(e2, ErrOutOfBounds),
		errors.Is(e3, ErrInvalidRadius), got == crel.Contained}
	for i, ok := range oks {
		if !ok {
			return fmt.Errorf("selfcheck: %s check failed", names[i])
		}
	}
	return nil
}
