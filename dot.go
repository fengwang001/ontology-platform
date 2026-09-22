package ontology

import "math"

// Dot returns the dot product of two sparse vectors using a single
// two-pointer merge over the index sequences. Inputs are never modified
// and never expanded into dense arrays.
//
// Products are accumulated with Neumaier's compensated summation, so
// the result is stable when term magnitudes differ wildly. Terms in
// which either factor is an explicit zero are skipped, so stored zeros
// never affect the result (even opposite an infinite value). If the
// final sum is Inf or NaN a *Error with Kind KindNonFinite is returned.
func Dot(a, b Vector) (float64, Stats, error) {
	zeros, err := validatePair(a, b)
	if err != nil {
		return 0, Stats{}, err
	}
	sum, steps := mergeDot(a, b)
	if math.IsNaN(sum) || math.IsInf(sum, 0) {
		return 0, Stats{}, newError(KindNonFinite, -1, -1,
			"dot product is not finite")
	}
	return sum, Stats{Steps: steps, ExplicitZeros: zeros}, nil
}

// mergeDot performs the two-pointer merge. Each loop iteration counts
// as one step, so the cost is O(len(a)+len(b)) regardless of how large
// the dimension subscripts are.
func mergeDot(a, b Vector) (float64, int) {
	var sum, comp float64
	steps := 0
	i, j := 0, 0
	for i < len(a.Indices) && j < len(b.Indices) {
		steps++
		switch ai, bi := a.Indices[i], b.Indices[j]; {
		case ai < bi:
			i++
		case ai > bi:
			j++
		default:
			av, bv := a.Values[i], b.Values[j]
			i++
			j++
			if av == 0 || bv == 0 {
				continue
			}
			sum, comp = neumaierAdd(sum, comp, av*bv)
		}
	}
	return sum + comp, steps
}

// neumaierAdd adds term t to a running sum with Neumaier's improvement
// of Kahan compensation: the lost low-order bits accumulate in comp.
func neumaierAdd(sum, comp, t float64) (float64, float64) {
	s := sum + t
	if math.Abs(sum) >= math.Abs(t) {
		comp += (sum - s) + t
	} else {
		comp += (t - s) + sum
	}
	return s, comp
}
