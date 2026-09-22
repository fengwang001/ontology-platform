package sparse

import "math"

// Dot computes the dot product of two sparse vectors with a single
// two-pointer merge. Inputs are never modified and never expanded
// into dense form.
//
// The summation uses Neumaier's compensated algorithm, and terms are
// always accumulated in ascending index order, so the result is
// deterministic and bitwise reproducible for a given pair of vectors
// regardless of how the inputs were assembled before sorting.
//
// Explicit zero entries contribute nothing: a product is only
// accumulated when both factors are non-zero.
//
// If the resulting sum is NaN or +/-Inf (possible when inputs contain
// infinities), a *NonFiniteError is returned.
func Dot(a, b Vector) (float64, Stats, error) {
	zeros, err := validatePair(a, b)
	if err != nil {
		return 0, Stats{}, err
	}
	dot, steps := mergeDot(a, b)
	stats := Stats{Steps: steps, ExplicitZeros: zeros}
	if math.IsNaN(dot) || math.IsInf(dot, 0) {
		return dot, stats, &NonFiniteError{What: "dot", Value: dot}
	}
	return dot, stats, nil
}

// mergeDot performs the two-pointer sweep over already-validated
// vectors and returns the compensated dot product plus the number of
// merge iterations performed.
func mergeDot(a, b Vector) (float64, int) {
	var sum, comp float64
	steps := 0
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		steps++
		switch {
		case a[i].Index < b[j].Index:
			i++
		case a[i].Index > b[j].Index:
			j++
		default:
			// Skip when either factor is an explicit zero: such
			// entries must never influence the result (this also
			// keeps 0*Inf from leaking NaN into the sum).
			if a[i].Value != 0 && b[j].Value != 0 {
				sum, comp = neumaier(sum, comp, a[i].Value*b[j].Value)
			}
			i++
			j++
		}
	}
	return sum + comp, steps
}

// neumaier adds x to the running compensated sum (sum, comp).
// It recovers the low-order bits lost when a small term is added to
// a much larger partial sum, keeping the total accurate even when
// term magnitudes differ by many orders of magnitude.
func neumaier(sum, comp, x float64) (float64, float64) {
	t := sum + x
	if math.Abs(sum) >= math.Abs(x) {
		comp += (sum - t) + x
	} else {
		comp += (x - t) + sum
	}
	return t, comp
}
