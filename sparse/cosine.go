package sparse

import "math"

// Cosine computes the cosine similarity of two sparse vectors with
// two-pointer merges only; inputs are never modified.
//
// Cosine is undefined when either vector has zero norm (empty or
// all-zero): a *ZeroNormError is returned instead of NaN or 0.
//
// When both self-norms and the dot product coincide bitwise (in
// particular whenever the two vectors are identical), the result is
// exactly 1. All other results are clamped into [-1, 1].
//
// A non-finite intermediate or result (possible with infinite input
// values) yields a *NonFiniteError.
func Cosine(a, b Vector) (float64, Stats, error) {
	zeros, err := validatePair(a, b)
	if err != nil {
		return 0, Stats{}, err
	}
	dotAB, stepsAB := mergeDot(a, b)
	normA2, stepsA := mergeDot(a, a)
	normB2, stepsB := mergeDot(b, b)
	stats := Stats{
		Steps:         stepsAB + stepsA + stepsB,
		ExplicitZeros: zeros,
	}

	if normA2 == 0 {
		return 0, stats, &ZeroNormError{Vector: 0}
	}
	if normB2 == 0 {
		return 0, stats, &ZeroNormError{Vector: 1}
	}
	if nonFinite(dotAB) || nonFinite(normA2) || nonFinite(normB2) {
		return 0, stats, &NonFiniteError{What: "cosine", Value: dotAB}
	}

	// Identical vectors (or any pair whose compensated sums coincide
	// bitwise) yield exactly 1, with no rounding drift.
	if dotAB == normA2 && normA2 == normB2 {
		return 1, stats, nil
	}

	r := dotAB / (math.Sqrt(normA2) * math.Sqrt(normB2))
	if nonFinite(r) {
		return 0, stats, &NonFiniteError{What: "cosine", Value: r}
	}
	if r > 1 {
		r = 1
	} else if r < -1 {
		r = -1
	}
	return r, stats, nil
}

func nonFinite(x float64) bool {
	return math.IsNaN(x) || math.IsInf(x, 0)
}
