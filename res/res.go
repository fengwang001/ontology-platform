// Package res holds the core set of active interval reservations and
// computes the peak load over a query window. It depends on no other package.
package res

import "sort"

// R is one active reservation: Need units held over the half-open interval [Start, End).
type R struct {
	Start, End, Need, ID int64
}

// Set stores active reservations as a sweep line over ordered endpoint
// coordinates. Events inside a query window are reached by binary search on
// the ordered coordinates (按端点有序定位), never by scanning every reservation.
//
// The unexported scanCount records how many active-reservation endpoints were
// inspected while computing the most recent peak. It is an unexported field
// and is never reachable through any exported field, function or method.
type Set struct {
	x         []int64 // sorted unique endpoint coordinates
	d         []int64 // net load delta applied at x[i]
	e         []int   // number of active reservation endpoints merged at x[i]
	p         []int64 // prefix load immediately after applying x[i]
	scanCount int
}

// Add inserts r's two endpoints into the sweep line.
func (s *Set) Add(r R) {
	s.apply(r.Start, r.Need, 1)
	s.apply(r.End, -r.Need, 1)
	s.rebuild()
}

// Remove erases r's two endpoints. Coordinates whose delta returns to zero
// are physically deleted, so a released reservation affects no later query.
func (s *Set) Remove(r R) {
	s.apply(r.Start, -r.Need, -1)
	s.apply(r.End, r.Need, -1)
	s.compact()
	s.rebuild()
}

// apply merges one endpoint into the ordered coordinate arrays (O(m) shift).
func (s *Set) apply(x, delta int64, endpoints int) {
	i := sort.Search(len(s.x), func(i int) bool { return s.x[i] >= x })
	if i < len(s.x) && s.x[i] == x {
		s.d[i] += delta
		s.e[i] += endpoints
		return
	}
	s.x = append(s.x, 0)
	copy(s.x[i+1:], s.x[i:])
	s.x[i] = x
	s.d = append(s.d, 0)
	copy(s.d[i+1:], s.d[i:])
	s.d[i] = delta
	s.e = append(s.e, 0)
	copy(s.e[i+1:], s.e[i:])
	s.e[i] = endpoints
}

// compact drops coordinates with no remaining endpoints.
func (s *Set) compact() {
	k := 0
	for i := range s.x {
		if s.e[i] != 0 {
			s.x[k], s.d[k], s.e[k] = s.x[i], s.d[i], s.e[i]
			k++
		}
	}
	s.x, s.d, s.e = s.x[:k], s.d[:k], s.e[:k]
}

// rebuild recomputes prefix loads after the coordinate arrays changed.
func (s *Set) rebuild() {
	if cap(s.p) < len(s.x) {
		s.p = make([]int64, len(s.x))
	} else {
		s.p = s.p[:len(s.x)]
	}
	var run int64
	for i := range s.x {
		run += s.d[i]
		s.p[i] = run
	}
}

// Peak returns max load(t) over integer points t in [start, end): the sum of
// Need of active reservations covering t. The only reservation endpoints
// inspected are those located strictly inside the window; locating the window
// itself is a binary search over coordinates.
func (s *Set) Peak(start, end int64) int64 {
	s.scanCount = 0
	i := sort.Search(len(s.x), func(i int) bool { return s.x[i] > start })
	var load int64
	if i > 0 {
		load = s.p[i-1] // load at point start: every coordinate <= start applied
	}
	peak := load
	for ; i < len(s.x) && s.x[i] < end; i++ {
		load += s.d[i]
		s.scanCount += s.e[i]
		if load > peak {
			peak = load
		}
	}
	return peak
}

// LastPeakCheckBounded answers whether the most recent Peak compared no more
// than bound active reservations. It deliberately exposes only this boolean
// property: the counter value itself is unexported and cannot be read through
// any exported field, function or method.
func (s *Set) LastPeakCheckBounded(bound int) bool { return s.scanCount <= bound }
