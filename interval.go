package ontology

import "time"

// All intervals in this package are half-open: [from, to). A zero "to"
// means +infinity. A zero "from" is never valid for stored facts.

// cmpTo compares two interval ends, treating the zero time as +infinity.
func cmpTo(a, b time.Time) int {
	switch {
	case a.IsZero() && b.IsZero():
		return 0
	case a.IsZero():
		return 1
	case b.IsZero():
		return -1
	default:
		return a.Compare(b)
	}
}

// beforeTo reports whether point p is strictly before interval end "to",
// where a zero "to" is +infinity.
func beforeTo(p, to time.Time) bool {
	return to.IsZero() || p.Before(to)
}

// contains reports whether p lies in [from, to): inclusive on the left,
// exclusive on the right.
func contains(from, to, p time.Time) bool {
	if p.Before(from) {
		return false
	}
	return beforeTo(p, to)
}

// overlaps reports whether [af, at) and [bf, bt) share at least one point.
func overlaps(af, at, bf, bt time.Time) bool {
	return beforeTo(af, bt) && beforeTo(bf, at)
}
