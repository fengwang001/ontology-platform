// Package seqset implements an ordered set of disjoint, inclusive integer
// intervals with per-interval attributes. It is the storage backbone of the
// telemetry backfill planner: missing, in-flight and lost sequence numbers
// are each kept in their own Set.
//
// Intervals are stored in a sorted slice and located by binary search. The
// non-exported counter visited records how many interval nodes an operation
// examined (binary-search probes); bulk slice moves are not examinations.
package seqset

// Attr holds per-interval attributes.
type Attr struct {
	C        int   // number of times the sequence numbers were requested
	Inflight bool  // whether the interval is part of an in-flight request
	Q        int64 // request timestamp in ms, meaningful when Inflight
	Gen      int64 // generation, used to detect stale in-flight heap entries
}

// Interval is a closed range [Start, End] with attributes.
type Interval struct {
	Start, End int64
	Attr
}

// Len returns the number of sequence numbers covered by the interval.
func (iv Interval) Len() int64 { return iv.End - iv.Start + 1 }

// Set is an ordered collection of disjoint intervals sorted by Start.
// The zero value is ready to use.
type Set struct {
	ivs     []Interval
	total   int64
	visited int
}

// Len returns the number of intervals (segments) in the set.
func (s *Set) Len() int { return len(s.ivs) }

// Total returns the number of sequence numbers covered by the set.
func (s *Set) Total() int64 { return s.total }

// Visited returns how many interval nodes were examined since the last
// ResetVisited.
func (s *Set) Visited() int { return s.visited }

// ResetVisited clears the examination counter.
func (s *Set) ResetVisited() { s.visited = 0 }

// At returns the i-th interval.
func (s *Set) At(i int) Interval { return s.ivs[i] }

// SetAttr replaces the attributes of the i-th interval.
func (s *Set) SetAttr(i int, a Attr) { s.ivs[i].Attr = a }

// Min returns the interval with the smallest Start.
func (s *Set) Min() (Interval, bool) {
	if len(s.ivs) == 0 {
		return Interval{}, false
	}
	return s.ivs[0], true
}

// searchPos returns the index of the first interval with Start >= x.
func (s *Set) searchPos(x int64) int {
	lo, hi := 0, len(s.ivs)
	for lo < hi {
		mid := (lo + hi) / 2
		s.visited++
		if s.ivs[mid].Start < x {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// Find returns the index of the interval containing seq.
func (s *Set) Find(seq int64) (int, bool) {
	i := s.searchPos(seq)
	if i < len(s.ivs) && s.ivs[i].Start == seq {
		return i, true
	}
	if i > 0 && seq <= s.ivs[i-1].End {
		return i - 1, true
	}
	return -1, false
}

// insertAt inserts iv at index i. The caller guarantees no overlap.
func (s *Set) insertAt(i int, iv Interval) {
	s.ivs = append(s.ivs, Interval{})
	copy(s.ivs[i+1:], s.ivs[i:])
	s.ivs[i] = iv
	s.total += iv.Len()
}

// Add inserts iv keeping the set sorted. The caller guarantees no overlap.
func (s *Set) Add(iv Interval) {
	s.insertAt(s.searchPos(iv.Start), iv)
}

// AddMerged inserts iv and merges it with an adjacent neighbor (on either
// side, or both) when the neighbor carries identical attributes.
func (s *Set) AddMerged(iv Interval) {
	pos := s.searchPos(iv.Start)
	if left := pos - 1; left >= 0 && s.ivs[left].Attr == iv.Attr && s.ivs[left].End+1 == iv.Start {
		s.ivs[left].End = iv.End
		s.total += iv.Len()
		if pos < len(s.ivs) && s.ivs[pos].Attr == iv.Attr && s.ivs[pos].Start == iv.End+1 {
			s.ivs[left].End = s.ivs[pos].End
			s.removeAt(pos)
		}
		return
	}
	if pos < len(s.ivs) && s.ivs[pos].Attr == iv.Attr && s.ivs[pos].Start == iv.End+1 {
		s.ivs[pos].Start = iv.Start
		s.total += iv.Len()
		return
	}
	s.insertAt(pos, iv)
}

// removeAt deletes the i-th interval, accounting its length.
func (s *Set) removeAt(i int) {
	s.total -= s.ivs[i].Len()
	copy(s.ivs[i:], s.ivs[i+1:])
	s.ivs = s.ivs[:len(s.ivs)-1]
}

// RemoveAt deletes the i-th interval.
func (s *Set) RemoveAt(i int) { s.removeAt(i) }

// ShrinkStart removes n sequence numbers from the front of the i-th
// interval. The caller guarantees n < interval length.
func (s *Set) ShrinkStart(i int, n int64) {
	s.ivs[i].Start += n
	s.total -= n
}

// RemovePointAt removes seq from the i-th interval (which must contain it),
// shrinking or splitting the interval. It returns the interval's attributes
// and the index range [lo, hi) of the surviving pieces.
func (s *Set) RemovePointAt(i int, seq int64) (attr Attr, lo, hi int) {
	iv := s.ivs[i]
	attr = iv.Attr
	s.total--
	switch {
	case iv.Start == seq && iv.End == seq:
		s.removeAt(i)
		s.total++ // removeAt accounted the whole interval; only one seq left
		return attr, i, i
	case iv.Start == seq:
		s.ivs[i].Start++
		return attr, i, i + 1
	case iv.End == seq:
		s.ivs[i].End--
		return attr, i, i + 1
	default:
		right := Interval{Start: seq + 1, End: iv.End, Attr: iv.Attr}
		s.ivs[i].End = seq - 1
		s.ivs = append(s.ivs, Interval{})
		copy(s.ivs[i+2:], s.ivs[i+1:])
		s.ivs[i+1] = right
		return attr, i, i + 2
	}
}

// RemoveBefore removes every sequence number < limit. It returns the removed
// pieces and, when the first surviving interval was split, that remainder
// (with unchanged attributes).
func (s *Set) RemoveBefore(limit int64) (removed []Interval, rem Interval, hasRem bool) {
	n := 0
	for n < len(s.ivs) && s.ivs[n].End < limit {
		removed = append(removed, s.ivs[n])
		s.total -= s.ivs[n].Len()
		n++
	}
	copy(s.ivs, s.ivs[n:])
	s.ivs = s.ivs[:len(s.ivs)-n]
	if len(s.ivs) > 0 && s.ivs[0].Start < limit {
		iv := s.ivs[0]
		removed = append(removed, Interval{Start: iv.Start, End: limit - 1, Attr: iv.Attr})
		s.ivs[0].Start = limit
		s.total -= limit - iv.Start
		rem, hasRem = s.ivs[0], true
	}
	return removed, rem, hasRem
}
