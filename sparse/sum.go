package sparse

import "math"

// compensator implements Neumaier's improved Kahan compensated
// summation. It keeps a running compensation term so that adding terms
// of wildly different magnitudes (e.g. 1e16 and 1) does not silently
// discard the small terms.
//
// Because the merge always feeds products in strictly ascending index
// order, the summation order is fully determined by the index sequence,
// not by how the input slices were assembled: results are bitwise
// reproducible for a given pair of sorted vectors.
type compensator struct {
	sum float64
	c   float64
}

func (s *compensator) add(x float64) {
	t := s.sum + x
	if math.Abs(s.sum) >= math.Abs(x) {
		s.c += (s.sum - t) + x
	} else {
		s.c += (x - t) + s.sum
	}
	s.sum = t
}

func (s *compensator) total() float64 {
	return s.sum + s.c
}
