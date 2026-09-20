package quantile

import (
	"math"
	"sort"
)

// Add records value with the given positive integer weight. If weight is
// omitted it defaults to 1.
//
// Weight semantics are exact replication without expansion: Add(v, 3) is
// equivalent to three Add(v, 1) calls and only a single distinct value is
// stored.
//
// A NaN value is rejected with ErrNaNValue and counted in SkippedNaN; it does
// not affect UniqueCount, TotalWeight or any query result. +0.0 and -0.0 are
// treated as the same value: their weights merge and the stored value is
// reported as +0.0. Infinities (either sign) are legal samples.
//
// More than one weight argument is rejected with ErrInvalidWeight.
func (s *Sketch) Add(value float64, weight ...uint64) error {
	w := uint64(1)
	switch len(weight) {
	case 0:
	case 1:
		w = weight[0]
		if w == 0 {
			return ErrInvalidWeight
		}
	default:
		return ErrInvalidWeight
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if math.IsNaN(value) {
		s.skippedNaN++
		return ErrNaNValue
	}
	if value == 0 {
		value = 0 // canonicalize -0.0 to +0.0
	}

	i := sort.Search(len(s.points), func(i int) bool {
		return s.points[i].value >= value
	})
	if i < len(s.points) && s.points[i].value == value {
		s.points[i].weight += w
		s.total += w
		return nil
	}

	s.points = append(s.points, point{})
	copy(s.points[i+1:], s.points[i:])
	s.points[i] = point{value: value, weight: w}
	s.total += w
	return nil
}

// UniqueCount returns the number of distinct values currently stored.
func (s *Sketch) UniqueCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.points)
}

// TotalWeight returns the sum of all accepted weights.
func (s *Sketch) TotalWeight() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.total
}

// SkippedNaN returns how many Add calls were rejected because their value was
// NaN.
func (s *Sketch) SkippedNaN() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.skippedNaN
}
