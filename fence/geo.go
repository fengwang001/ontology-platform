package fence

import "fmt"

// maxCoordinate bounds accepted coordinates so that every cross product
// below stays within int64: |coord| <= 2^29 keeps any product of two
// coordinate differences <= 2^60 and any cross product <= 2^61.
const maxCoordinate = int64(1) << 29

func coordOK(p Point) bool {
	return p.X >= -maxCoordinate && p.X <= maxCoordinate &&
		p.Y >= -maxCoordinate && p.Y <= maxCoordinate
}

// cross returns the z-component of (b-a) x (c-a): positive when c is left
// of the directed edge a->b, zero when collinear.
func cross(a, b, c Point) int64 {
	return (b.X-a.X)*(c.Y-a.Y) - (b.Y-a.Y)*(c.X-a.X)
}

// onSegment reports whether p lies on the closed segment ab.
func onSegment(a, b, p Point) bool {
	if cross(a, b, p) != 0 {
		return false
	}
	return p.X >= min(a.X, b.X) && p.X <= max(a.X, b.X) &&
		p.Y >= min(a.Y, b.Y) && p.Y <= max(a.Y, b.Y)
}

// pointInPolygon is an exact integer ray-casting test. Points on the
// boundary (edges and vertices) count as inside.
func pointInPolygon(poly []Point, p Point) bool {
	inside := false
	n := len(poly)
	for i := 0; i < n; i++ {
		a := poly[i]
		b := poly[(i+1)%n]
		if onSegment(a, b, p) {
			return true
		}
		if (a.Y > p.Y) != (b.Y > p.Y) {
			// The edge crosses the horizontal line y = p.Y. Compare p.X
			// with the intersection x without dividing:
			//   p.X < xint  <=>  (p.X-a.X)*(b.Y-a.Y) vs (p.Y-a.Y)*(b.X-a.X)
			// with the comparison flipped when (b.Y-a.Y) is negative.
			lhs := (p.X - a.X) * (b.Y - a.Y)
			rhs := (p.Y - a.Y) * (b.X - a.X)
			switch {
			case b.Y > a.Y && lhs < rhs:
				inside = !inside
			case b.Y < a.Y && lhs > rhs:
				inside = !inside
			}
		}
	}
	return inside
}

// segmentsIntersect reports whether closed segments ab and cd share any
// point, including endpoint touches and collinear overlaps.
func segmentsIntersect(a, b, c, d Point) bool {
	o1 := cross(a, b, c)
	o2 := cross(a, b, d)
	o3 := cross(c, d, a)
	o4 := cross(c, d, b)
	if ((o1 > 0 && o2 < 0) || (o1 < 0 && o2 > 0)) &&
		((o3 > 0 && o4 < 0) || (o3 < 0 && o4 > 0)) {
		return true
	}
	return (o1 == 0 && onSegment(a, b, c)) ||
		(o2 == 0 && onSegment(a, b, d)) ||
		(o3 == 0 && onSegment(c, d, a)) ||
		(o4 == 0 && onSegment(c, d, b))
}

// properCross reports whether segments ab and cd intersect in their
// relative interiors (touching endpoints do not count).
func properCross(a, b, c, d Point) bool {
	o1 := cross(a, b, c)
	o2 := cross(a, b, d)
	o3 := cross(c, d, a)
	o4 := cross(c, d, b)
	return ((o1 > 0 && o2 < 0) || (o1 < 0 && o2 > 0)) &&
		((o3 > 0 && o4 < 0) || (o3 < 0 && o4 > 0))
}

// validateSimplePolygon checks that poly is a usable simple polygon:
// at least three vertices, no coincident adjacent vertices (including the
// wrap-around pair), no backtracking spikes, and no intersections between
// non-adjacent edges.
func validateSimplePolygon(poly []Point) error {
	n := len(poly)
	if n < 3 {
		return fmt.Errorf("polygon needs at least 3 vertices, got %d", n)
	}
	for i := 0; i < n; i++ {
		if !coordOK(poly[i]) {
			return fmt.Errorf("vertex %d out of coordinate range", i)
		}
		if poly[i] == poly[(i+1)%n] {
			return fmt.Errorf("adjacent vertices %d and %d coincide", i, (i+1)%n)
		}
	}
	edge := func(i int) (Point, Point) { return poly[i], poly[(i+1)%n] }
	for i := 0; i < n; i++ {
		// Reject spikes: consecutive collinear edges that fold back.
		prev := poly[(i+n-1)%n]
		cur := poly[i]
		next := poly[(i+1)%n]
		if cross(prev, cur, next) == 0 {
			dot := (cur.X-prev.X)*(next.X-cur.X) + (cur.Y-prev.Y)*(next.Y-cur.Y)
			if dot < 0 {
				return fmt.Errorf("polygon backtracks at vertex %d", i)
			}
		}
		for j := i + 1; j < n; j++ {
			adjacent := j == i+1 || (i == 0 && j == n-1)
			if adjacent {
				continue
			}
			a1, a2 := edge(i)
			b1, b2 := edge(j)
			if segmentsIntersect(a1, a2, b1, b2) {
				return fmt.Errorf("edges %d and %d intersect", i, j)
			}
		}
	}
	return nil
}

// containsPolygon reports whether the simple polygon inner lies completely
// inside the simple polygon outer; boundary contact is allowed.
func containsPolygon(outer, inner []Point) bool {
	for _, v := range inner {
		if !pointInPolygon(outer, v) {
			return false
		}
	}
	for i := 0; i < len(inner); i++ {
		a := inner[i]
		b := inner[(i+1)%len(inner)]
		for j := 0; j < len(outer); j++ {
			c := outer[j]
			d := outer[(j+1)%len(outer)]
			if properCross(a, b, c, d) {
				return false
			}
		}
	}
	return true
}

// polygonsConflict reports whether two simple polygons share any point,
// including boundary touches and full containment.
func polygonsConflict(a, b []Point) bool {
	for i := 0; i < len(a); i++ {
		a1 := a[i]
		a2 := a[(i+1)%len(a)]
		for j := 0; j < len(b); j++ {
			b1 := b[j]
			b2 := b[(j+1)%len(b)]
			if segmentsIntersect(a1, a2, b1, b2) {
				return true
			}
		}
	}
	return pointInPolygon(a, b[0]) || pointInPolygon(b, a[0])
}

func bbox(poly []Point) (minX, minY, maxX, maxY int64) {
	minX, maxX = poly[0].X, poly[0].X
	minY, maxY = poly[0].Y, poly[0].Y
	for _, p := range poly[1:] {
		minX = min(minX, p.X)
		minY = min(minY, p.Y)
		maxX = max(maxX, p.X)
		maxY = max(maxY, p.Y)
	}
	return
}
