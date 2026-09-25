package sparse

import "math"

// Dot returns the dot product of two sparse vectors.
//
// The computation is a single two-pointer merge over the two index
// sequences; the vectors are never expanded into dense arrays. Matched
// products are accumulated with Neumaier compensated summation in
// strictly ascending index order, so the result is independent of how
// the input slices were assembled and is reproducible bit-for-bit.
//
// If the result would be NaN or +/-Inf (e.g. inputs containing
// infinities), a *NonFiniteError is returned instead of the value.
func Dot(a, b Vector) (float64, Stats, error) {
	st, err := validateBoth(a, b)
	if err != nil {
		return 0, st, err
	}
	dot := mergeDot(a, b, &st)
	if math.IsNaN(dot) || math.IsInf(dot, 0) {
		return 0, st, &NonFiniteError{Context: "dot", Value: dot}
	}
	return dot, st, nil
}

// mergeDot performs the two-pointer merge. Every loop iteration counts
// as one step; iterations are bounded by len(a)+len(b).
func mergeDot(a, b Vector, st *Stats) float64 {
	var acc compensator
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		st.Steps++
		switch {
		case a[i].Index < b[j].Index:
			i++
		case a[i].Index > b[j].Index:
			j++
		default:
			acc.add(a[i].Value * b[j].Value)
			i++
			j++
		}
	}
	return acc.total()
}
