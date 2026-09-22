package ontology

import "math"

// Cosine computes the cosine similarity of two sparse vectors with a
// single two-pointer merge for the shared dot product plus one linear
// pass per vector for the norms. Inputs are never modified.
//
// Edge cases are pinned down:
//   - a zero-norm vector (empty or all zeros) yields *ZeroNormError,
//     never a NaN or a silent 0;
//   - when the dot product and both squared norms are bit-identical
//     (in particular for two identical vectors) the result is exactly
//     1, bypassing the sqrt/division that could give 1.0000000000000002;
//   - the result is clamped into [-1, 1].
func Cosine(a, b SparseVector) (float64, Stats, error) {
	var st Stats
	if err := validate(a, First, &st); err != nil {
		return 0, st, err
	}
	if err := validate(b, Second, &st); err != nil {
		return 0, st, err
	}

	suma := sumSquares(a)
	sumb := sumSquares(b)
	switch {
	case suma == 0 && sumb == 0:
		return 0, st, &ZeroNormError{Side: Both}
	case suma == 0:
		return 0, st, &ZeroNormError{Side: First}
	case sumb == 0:
		return 0, st, &ZeroNormError{Side: Second}
	}

	sum, comp := 0.0, 0.0
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		st.Steps++
		switch {
		case a[i].Index < b[j].Index:
			i++
		case a[i].Index > b[j].Index:
			j++
		default:
			sum, comp = neumaier(sum, comp, a[i].Value*b[j].Value)
			i++
			j++
		}
	}
	dot := sum + comp

	// Identical vectors (and any pair with dot == suma == sumb
	// bitwise) are exactly parallel: return 1 without going through
	// sqrt, whose rounding could otherwise drift off 1.
	if dot == suma && suma == sumb {
		return 1, st, nil
	}

	cos := dot / (math.Sqrt(suma) * math.Sqrt(sumb))
	if cos > 1 {
		cos = 1
	} else if cos < -1 {
		cos = -1
	}
	return cos, st, nil
}
