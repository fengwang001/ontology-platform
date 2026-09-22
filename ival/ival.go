// Package ival defines half-open intervals [Lo, Hi) and their relations.
package ival

import "errors"

// ErrInvalid is returned when Lo > Hi.
var ErrInvalid = errors.New("ival: invalid interval (Lo > Hi)")

// Interval is a half-open interval [Lo, Hi) with a payload.
// Lo == Hi is the empty point set and is a valid interval.
type Interval struct {
	Lo      int64
	Hi      int64
	Payload string
}

// Valid reports whether Lo <= Hi.
func (i Interval) Valid() bool { return i.Lo <= i.Hi }

// Empty reports whether the interval contains no points.
func (i Interval) Empty() bool { return i.Lo == i.Hi }

// Contains reports whether point p lies in [Lo, Hi).
func (i Interval) Contains(p int64) bool { return i.Lo <= p && p < i.Hi }

// Overlaps reports whether a and b share at least one point:
// max(Lo) < min(Hi). Touching intervals do not overlap, and an
// empty interval never overlaps anything, including itself.
func Overlaps(a, b Interval) bool {
	return max(a.Lo, b.Lo) < min(a.Hi, b.Hi)
}

// Touches reports whether a and b are adjacent but disjoint:
// a.Hi == b.Lo or b.Hi == a.Lo.
func Touches(a, b Interval) bool {
	return a.Hi == b.Lo || b.Hi == a.Lo
}

// Intersect returns the intersection of a and b. The result is the
// empty interval [m, m) at m = max(Lo) when they do not overlap.
func Intersect(a, b Interval) Interval {
	lo, hi := max(a.Lo, b.Lo), min(a.Hi, b.Hi)
	if hi < lo {
		hi = lo
	}
	return Interval{Lo: lo, Hi: hi}
}

// Union returns the smallest interval covering both a and b.
func Union(a, b Interval) Interval {
	return Interval{Lo: min(a.Lo, b.Lo), Hi: max(a.Hi, b.Hi)}
}

// Compare orders intervals lexicographically by (Lo, Hi, Payload).
// It is the total order used both as tree key and as result sort order.
func Compare(a, b Interval) int {
	if a.Lo != b.Lo {
		if a.Lo < b.Lo {
			return -1
		}
		return 1
	}
	if a.Hi != b.Hi {
		if a.Hi < b.Hi {
			return -1
		}
		return 1
	}
	switch {
	case a.Payload < b.Payload:
		return -1
	case a.Payload > b.Payload:
		return 1
	}
	return 0
}
