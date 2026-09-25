// Package marz implements the endpoint generation and sweep-line core of
// Marzullo's clock-synchronization interval algorithm. It depends on nothing.
package marz

import "sort"

// Endpoint is one edge of a clock's error interval. Delta is +1 for the
// lower edge (entering) and -1 for the upper edge (leaving).
type Endpoint struct {
	Pos   int64
	Delta int
}

// Interval converts (offset, err) into the closed interval [lo, hi].
// Callers must guarantee err >= 0; validation lives in the api package.
func Interval(offset, err int64) (lo, hi int64) {
	return offset - err, offset + err
}

// Endpoints returns the two sweep endpoints of one clock's interval:
// L at offset-err (Delta +1) and R at offset+err (Delta -1).
func Endpoints(offset, err int64) (l, r Endpoint) {
	lo, hi := Interval(offset, err)
	return Endpoint{Pos: lo, Delta: 1}, Endpoint{Pos: hi, Delta: -1}
}

// Less reports whether endpoint a sorts before b: position ascending,
// and at the same position L (Delta +1) before R (Delta -1).
func Less(a, b Endpoint) bool {
	if a.Pos != b.Pos {
		return a.Pos < b.Pos
	}
	return a.Delta > b.Delta
}

// SortEndpoints sorts eps in place by Less.
func SortEndpoints(eps []Endpoint) {
	sort.Slice(eps, func(i, j int) bool { return Less(eps[i], eps[j]) })
}

// Sweep scans sorted endpoints and returns the shortest closed interval
// [lo, hi] covered by at least need intervals. ok is false when no point
// reaches count >= need. Ties keep the leftmost (first-found) interval.
func Sweep(eps []Endpoint, need int) (lo, hi int64, ok bool) {
	if need <= 0 {
		return 0, 0, false
	}
	count := 0
	open := false   // currently inside a candidate region
	var curLo int64 // left edge of the open candidate region
	best := int64(-1)
	for _, e := range eps {
		count += e.Delta
		switch {
		case count >= need && !open:
			open, curLo = true, e.Pos
		case count < need && open:
			open = false
			// Closed semantics: at e.Pos the leaving interval still
			// counts, so the candidate region is [curLo, e.Pos].
			if d := e.Pos - curLo; best < 0 || d < best {
				best, lo, hi = d, curLo, e.Pos
				ok = true
			}
		}
	}
	return lo, hi, ok
}
