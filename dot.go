package sparse

import "math"

// addComp adds term to (sum, comp) using Neumaier's compensated summation,
// so catastrophic cancellation between terms of wildly different magnitude
// (e.g. 1e16 and 1) does not silently lose the small terms.
func addComp(sum, comp, term float64) (float64, float64) {
	t := sum + term
	if math.Abs(sum) >= math.Abs(term) {
		comp += (sum - t) + term
	} else {
		comp += (term - t) + sum
	}
	return t, comp
}

// Dot returns the dot product of a and b in a single two-pointer merge.
// Neither input is modified. Summation runs in ascending index order with
// Neumaier compensation, so the result is deterministic and independent of
// how the inputs were physically laid out before sorting.
//
// It returns a *Error with Kind ErrNotSorted or ErrNaN for malformed input,
// and ErrNonFinite if the product overflows to Inf or collapses to NaN
// (possible when inputs contain ±Inf).
func Dot(a, b Vector) (float64, Stats, error) {
	za, err := validate("left", a)
	if err != nil {
		return 0, Stats{}, err
	}
	zb, err := validate("right", b)
	if err != nil {
		return 0, Stats{}, err
	}
	stats := Stats{ExplicitZeros: za + zb}

	sum, comp := 0.0, 0.0
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		stats.Steps++
		switch {
		case a[i].Index < b[j].Index:
			i++
		case a[i].Index > b[j].Index:
			j++
		default:
			sum, comp = addComp(sum, comp, a[i].Value*b[j].Value)
			i++
			j++
		}
	}
	dot := sum + comp
	if math.IsInf(dot, 0) || math.IsNaN(dot) {
		return 0, stats, nonFiniteErr("dot product", dot)
	}
	return dot, stats, nil
}
