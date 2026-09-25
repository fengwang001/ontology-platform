package sparse

import "math"

// Dot computes the dot product of two sparse vectors with exactly one
// two-pointer merge pass. The vectors are never expanded into dense arrays
// and never modified (no in-place sorting).
//
// Numeric strategy: the matched products are accumulated with Kahan
// compensated summation. The merge traverses the (already sorted) inputs in
// index order, which is fixed by the index sets rather than by how the
// caller originally produced the data, so the result is independent of the
// original element order. Kahan summation keeps the error at roughly one
// ulp even when term magnitudes differ by many orders of magnitude
// (e.g. 1e16 alongside 1).
//
// The returned Stats give the number of merge steps and the number of
// explicit zero elements across both vectors.
func Dot(a, b Vector) (Result, error) {
	zeros, err := validateBoth(a, b)
	if err != nil {
		return Result{}, err
	}

	sum, comp := 0.0, 0.0
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
			product := a[i].Value * b[j].Value
			addCompensated(&sum, &comp, product)
			i++
			j++
		}
	}

	if math.IsNaN(sum) {
		return Result{}, &Error{Reason: ReasonNonFiniteResult,
			Msg: "sparse: dot product is NaN"}
	}
	if math.IsInf(sum, 0) {
		return Result{}, &Error{Reason: ReasonNonFiniteResult,
			Msg: "sparse: dot product overflowed to infinity"}
	}

	return Result{
		Value: sum,
		Stats: Stats{Steps: steps, ExplicitZeros: zeros},
	}, nil
}

// addCompensated adds x to *sum using Kahan compensated summation.
// *comp carries the lost low-order bits of *sum.
func addCompensated(sum, comp *float64, x float64) {
	y := x - *comp
	t := *sum + y
	*comp = (t - *sum) - y
	*sum = t
}
