package sparse

import "math"

// Cosine computes the cosine similarity of two sparse vectors in a single
// two-pointer merge, accumulating dot(a,b), |a|^2 and |b|^2 together (each
// with Kahan compensated summation). No dense expansion occurs and the
// inputs are never modified.
//
// Boundary rules:
//   - if either vector has zero norm (empty or all zeros), the cosine is
//     undefined and an error wrapping ErrZeroNorm is returned;
//   - structurally identical vectors yield exactly 1.0 (identical Kahan
//     sums bit-for-bit, guaranteed by a direct identity fast path);
//   - the value is clipped to [-1, 1] to remove rounding overshoot;
//   - overflow to Inf/NaN during accumulation is a decidable error.
func Cosine(a, b Vector) (float64, Stats, error) {
	zeros, err := validateBoth(a, b)
	if err != nil {
		return 0, Stats{}, err
	}
	stats := Stats{ExplicitZeros: zeros}

	if sameStructure(a, b) {
		stats.Steps = len(a) // every index matches, both pointers advance each step
		return 1.0, stats, nil
	}

	dot, dotC := 0.0, 0.0
	na, naC := 0.0, 0.0
	nb, nbC := 0.0, 0.0
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		stats.Steps++
		switch {
		case a[i].Index < b[j].Index:
			addCompensated(&na, &naC, a[i].Value*a[i].Value)
			i++
		case a[i].Index > b[j].Index:
			addCompensated(&nb, &nbC, b[j].Value*b[j].Value)
			j++
		default:
			addCompensated(&dot, &dotC, a[i].Value*b[j].Value)
			addCompensated(&na, &naC, a[i].Value*a[i].Value)
			addCompensated(&nb, &nbC, b[j].Value*b[j].Value)
			i++
			j++
		}
	}
	for ; i < len(a); i++ {
		addCompensated(&na, &naC, a[i].Value*a[i].Value)
	}
	for ; j < len(b); j++ {
		addCompensated(&nb, &nbC, b[j].Value*b[j].Value)
	}

	if math.IsNaN(dot) || math.IsNaN(na) || math.IsNaN(nb) {
		return 0, stats, &Error{Reason: ReasonNonFiniteResult,
			Msg: "sparse: cosine accumulation produced NaN"}
	}
	if math.IsInf(dot, 0) || math.IsInf(na, 0) || math.IsInf(nb, 0) {
		return 0, stats, &Error{Reason: ReasonNonFiniteResult,
			Msg: "sparse: cosine accumulation overflowed"}
	}
	if na == 0 || nb == 0 {
		return 0, stats, &Error{Reason: ReasonZeroNorm,
			Msg: "sparse: cosine undefined: zero-norm vector (empty or all zeros)"}
	}

	denom := math.Sqrt(na) * math.Sqrt(nb)
	cos := dot / denom
	if math.IsNaN(cos) || math.IsInf(cos, 0) {
		return 0, stats, &Error{Reason: ReasonNonFiniteResult,
			Msg: "sparse: cosine result is non-finite"}
	}
	if cos > 1 {
		cos = 1
	}
	if cos < -1 {
		cos = -1
	}
	return cos, stats, nil
}

// sameStructure reports whether the two vectors contain the exact same
// (index, value) pairs in the same order.
func sameStructure(a, b Vector) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
