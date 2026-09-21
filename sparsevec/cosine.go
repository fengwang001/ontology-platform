package sparsevec

import "math"

// Cosine returns the cosine similarity of a and b plus merge statistics.
//
// It is undefined when either vector has zero norm (empty or all-zero);
// that case returns ErrZeroNorm instead of NaN. The result is clamped to
// [-1, 1], and identical vectors yield exactly 1.
func Cosine(a, b Vector) (float64, Stats, error) {
	st, err := check(a, b)
	if err != nil {
		return 0, Stats{}, err
	}
	var dotAcc, aAcc, bAcc neumaier
	ms := merge(a, b, func(av, bv float64) {
		dotAcc.add(av * bv)
	})
	st.Steps = ms.steps
	for _, e := range a {
		aAcc.add(e.Value * e.Value)
	}
	for _, e := range b {
		bAcc.add(e.Value * e.Value)
	}
	dot, sa, sb := dotAcc.total(), aAcc.total(), bAcc.total()
	for _, v := range []float64{dot, sa, sb} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, Stats{}, nonFiniteErr("cosine component", v)
		}
	}
	if sa == 0 {
		return 0, Stats{}, zeroNormErr(0)
	}
	if sb == 0 {
		return 0, Stats{}, zeroNormErr(1)
	}
	// Bitwise-identical self-similarity: dot == sa == sb, so the true
	// value is exactly 1; return it without dividing rounded norms.
	if dot == sa && sa == sb {
		return 1, st, nil
	}
	cos := dot / (math.Sqrt(sa) * math.Sqrt(sb))
	if cos > 1 {
		cos = 1
	} else if cos < -1 {
		cos = -1
	}
	return cos, st, nil
}
