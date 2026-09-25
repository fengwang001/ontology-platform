package sparse

import "math"

// Cosine returns the cosine similarity of two sparse vectors.
//
// Boundary behavior:
//   - zero-norm vector (empty or all values zero): *ZeroNormError,
//     never NaN or 0;
//   - non-finite intermediate or result (e.g. +/-Inf in the input):
//     *NonFiniteError, never NaN;
//   - element-wise identical vectors: exactly 1.0, guaranteed by an
//     explicit equality fast path (after validation and norm checks),
//     so no floating-point drift can produce 1.0000000000000002;
//   - otherwise the result is clamped into [-1, 1].
//
// The dot part uses the same single two-pointer merge as Dot; each
// norm is one pass with compensated summation. Inputs are never
// modified.
func Cosine(a, b Vector) (float64, Stats, error) {
	st, err := validateBoth(a, b)
	if err != nil {
		return 0, st, err
	}
	na2 := normSquared(a)
	nb2 := normSquared(b)
	if na2 == 0 {
		return 0, st, &ZeroNormError{Vector: 0}
	}
	if nb2 == 0 {
		return 0, st, &ZeroNormError{Vector: 1}
	}
	dot := mergeDot(a, b, &st)
	if nonFinite(dot) || nonFinite(na2) || nonFinite(nb2) {
		return 0, st, &NonFiniteError{Context: "cosine", Value: firstNonFinite(dot, na2, nb2)}
	}
	if equalElements(a, b) {
		return 1, st, nil
	}
	cos := dot / (math.Sqrt(na2) * math.Sqrt(nb2))
	if nonFinite(cos) {
		return 0, st, &NonFiniteError{Context: "cosine", Value: cos}
	}
	return clamp(cos, -1, 1), st, nil
}

// normSquared computes sum(v_i^2) with compensated summation.
func normSquared(v Vector) float64 {
	var acc compensator
	for _, e := range v {
		acc.add(e.Value * e.Value)
	}
	return acc.total()
}

func equalElements(a, b Vector) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Index != b[i].Index || a[i].Value != b[i].Value {
			return false
		}
	}
	return true
}

func nonFinite(x float64) bool {
	return math.IsNaN(x) || math.IsInf(x, 0)
}

func firstNonFinite(xs ...float64) float64 {
	for _, x := range xs {
		if nonFinite(x) {
			return x
		}
	}
	return math.NaN()
}

func clamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}
