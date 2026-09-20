package quantile

import "math"

// validateProbability rejects probabilities outside [0,1], including NaN.
// It never clamps to the boundary.
func validateProbability(p float64) error {
	if p != p || p < 0 || p > 1 || math.IsInf(p, 0) {
		return ErrInvalidProbability
	}
	return nil
}

// snapshot returns a defensive copy of the sorted points so that queries can
// never observe or mutate internal state.
func (s *Sketch) snapshot() ([]point, uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.points) == 0 {
		return nil, 0, ErrEmpty
	}
	out := make([]point, len(s.points))
	copy(out, s.points)
	return out, s.total, nil
}

// QuantileNearestRank returns the p-quantile using the nearest-rank method.
//
// With N total (weighted) observations sorted as x_1 <= ... <= x_N, the rank
// is r = ceil(p*N), clamped to [1,N], and the result is x_r. The returned
// value is therefore always a value actually present in the sample. p=0
// returns the minimum and p=1 the maximum.
func (s *Sketch) QuantileNearestRank(p float64) (float64, error) {
	if err := validateProbability(p); err != nil {
		return 0, err
	}
	pts, total, err := s.snapshot()
	if err != nil {
		return 0, err
	}
	r := ceilRank(p, total)
	return valueAtRank(pts, r), nil
}

// QuantileLinear returns the p-quantile using the Hyndman-Fan type 7 (R-7)
// definition: h = p*(N-1)+1 gives a 1-based fractional rank; with floor f and
// fraction g = h-f the result is x_f + g*(x_{f+1}-x_f). p=0 yields the
// minimum and p=1 the maximum, both real sample values.
//
// Infinity policy: interpolating between an infinite endpoint and a finite
// value is defined here to return the infinite endpoint (for any nonzero
// interpolation weight), instead of producing NaN from Inf - Inf or
// Inf*0. Interpolating between -Inf and +Inf has no finite definition and
// returns NaN. When the fraction is exactly 0 or 1 an actual sample value is
// returned.
func (s *Sketch) QuantileLinear(p float64) (float64, error) {
	if err := validateProbability(p); err != nil {
		return 0, err
	}
	pts, total, err := s.snapshot()
	if err != nil {
		return 0, err
	}

	// h is the 1-based continuous rank in [1,N].
	h := 1 + p*(float64(total)-1)
	low := uint64(h) // floor; safe because h is finite and in [1,N]
	if low < 1 {
		low = 1
	}
	if low >= total {
		return valueAtRank(pts, total), nil
	}
	g := fractionalPart(h, low)
	if g == 0 {
		return valueAtRank(pts, low), nil
	}

	x0 := valueAtRank(pts, low)
	x1 := valueAtRank(pts, low+1)
	return interpolate(x0, x1, g), nil
}

// ceilRank computes ceil(p*N) in 1-based ranks, clamped to [1,N], using
// integer arithmetic to avoid floating-point overshoot at p=1.
func ceilRank(p float64, n uint64) uint64 {
	switch {
	case p == 0:
		return 1
	case p == 1:
		return n
	}
	// r = ceil(p*n). A remainder below relative epsilon is treated as an exact
	// integer rank so that a p landing on a sample point is recognized
	// bit-stably instead of being pushed one rank up by float rounding.
	h := p * float64(n)
	r := uint64(h)
	if r < 1 {
		return 1
	}
	if fractionalPart(h, r) > 0 {
		r++
	}
	if r > n {
		return n
	}
	return r
}

// fractionalPart returns h-floor(h), collapsing remainders at or below one
// part in 1e12 to zero so that an ideally-integer rank is not perturbed by
// floating-point rounding.
func fractionalPart(h float64, floor uint64) float64 {
	rem := h - float64(floor)
	if rem <= 1e-12*h {
		return 0
	}
	return rem
}

// valueAtRank returns the sample value covering 1-based rank r over points
// sorted ascending with their weights.
func valueAtRank(pts []point, r uint64) float64 {
	var cum uint64
	for _, q := range pts {
		cum += q.weight
		if r <= cum {
			return q.value
		}
	}
	return pts[len(pts)-1].value
}

// interpolate returns x0 + g*(x1-x0) with the infinity policy documented on
// QuantileLinear.
func interpolate(x0, x1, g float64) float64 {
	i0 := math.IsInf(x0, 0)
	i1 := math.IsInf(x1, 0)
	switch {
	case i0 && i1:
		if math.Signbit(x0) == math.Signbit(x1) {
			return x0
		}
		return math.NaN() // -Inf to +Inf is undefined
	case i0:
		return x0 // any nonzero weight on the infinite lower endpoint
	case i1:
		return x1
	default:
		return x0 + g*(x1-x0)
	}
}
