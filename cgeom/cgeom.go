// Package cgeom provides integer-exact geometric predicates. No floats.
package cgeom

import "math/bits"

// Point is an integer-lattice point.
type Point struct {
	X, Y int
}

// Rat2 is an exact non-negative squared distance: value = Num/Den, Den > 0.
// Endpoint cases use Den == 1; an interior perpendicular foot whose squared
// distance is fractional keeps Num = cross^2 and Den = segment length^2.
type Rat2 struct {
	Num, Den int64
}

// CmpRat2 compares two exact squared distances without overflow, using
// 128-bit cross multiplication (a/b vs c/d  <=>  a*d vs c*b).
func CmpRat2(a, b Rat2) int {
	ah, al := bits.Mul64(uint64(a.Num), uint64(b.Den))
	bh, bl := bits.Mul64(uint64(b.Num), uint64(a.Den))
	if ah != bh {
		if ah < bh {
			return -1
		}
		return 1
	}
	if al != bl {
		if al < bl {
			return -1
		}
		return 1
	}
	return 0
}

// CmpRat2Int compares an exact squared distance against integer n (n >= 0).
func CmpRat2Int(a Rat2, n int64) int {
	ah, al := bits.Mul64(uint64(a.Num), 1)
	nh, nl := bits.Mul64(uint64(n), uint64(a.Den))
	if ah != nh {
		if ah < nh {
			return -1
		}
		return 1
	}
	if al != nl {
		if al < nl {
			return -1
		}
		return 1
	}
	return 0
}

// PointSegDist2 returns the squared Euclidean distance from p to segment a-b.
// The point is projected onto the supporting line and the projection
// parameter is clamped to [0,1], so it is a point-to-SEGMENT distance.
func PointSegDist2(p, a, b Point) Rat2 {
	dx := int64(b.X - a.X)
	dy := int64(b.Y - a.Y)
	px := int64(p.X - a.X)
	py := int64(p.Y - a.Y)
	len2 := dx*dx + dy*dy
	if len2 == 0 { // degenerate segment: distance to the shared endpoint
		return Rat2{Num: px*px + py*py, Den: 1}
	}
	dot := px*dx + py*dy
	switch {
	case dot <= 0: // projection before a: clamp to a
		return Rat2{Num: px*px + py*py, Den: 1}
	case dot >= len2: // projection after b: clamp to b
		qx := int64(p.X - b.X)
		qy := int64(p.Y - b.Y)
		return Rat2{Num: qx*qx + qy*qy, Den: 1}
	default: // interior perpendicular foot: cross^2 / len^2
		cross := px*dy - py*dx
		return Rat2{Num: cross * cross, Den: len2}
	}
}

func onSeg(p, a, b Point) bool {
	dx := int64(b.X - a.X)
	dy := int64(b.Y - a.Y)
	cross := int64(p.X-a.X)*dy - int64(p.Y-a.Y)*dx
	if cross != 0 {
		return false
	}
	return min(a.X, b.X) <= p.X && p.X <= max(a.X, b.X) &&
		min(a.Y, b.Y) <= p.Y && p.Y <= max(a.Y, b.Y)
}

// PointInPoly reports whether p is inside or on the boundary of the simple
// polygon (ray casting to +x; boundary points count as inside). All arithmetic
// is integer: the ray/edge intersection is decided by cross multiplication.
func PointInPoly(p Point, poly []Point) bool {
	n := len(poly)
	inside := false
	for i := 0; i < n; i++ {
		a := poly[i]
		b := poly[(i+1)%n]
		if a == b {
			continue
		}
		if onSeg(p, a, b) {
			return true
		}
		// Does the edge straddle the horizontal ray y == p.Y?
		if (a.Y > p.Y) == (b.Y > p.Y) {
			continue
		}
		lo, hi := a, b
		if lo.Y > hi.Y {
			lo, hi = hi, lo
		}
		// p.X < x-intersection  <=>  (p.X-lo.X)*(hi.Y-lo.Y)
		//                            < (p.Y-lo.Y)*(hi.X-lo.X)
		lhs := int64(p.X-lo.X) * int64(hi.Y-lo.Y)
		rhs := int64(p.Y-lo.Y) * int64(hi.X-lo.X)
		if lhs < rhs {
			inside = !inside
		}
	}
	return inside
}
