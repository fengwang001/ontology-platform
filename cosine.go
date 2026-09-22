package ontology

import "math"

// Cosine returns the cosine similarity of two sparse vectors.
//
// A zero-norm vector (empty or all explicit zeros) makes the cosine
// undefined and yields a *Error with Kind KindZeroNorm. Non-finite
// intermediate results (infinite norms or dot products) yield
// KindNonFinite. When the two vectors are elementwise identical the
// result is exactly 1; otherwise it is clamped into [-1, 1].
func Cosine(a, b Vector) (float64, Stats, error) {
	zeros, err := validatePair(a, b)
	if err != nil {
		return 0, Stats{}, err
	}
	na := selfNorm(a)
	nb := selfNorm(b)
	if na == 0 || nb == 0 {
		which := 0
		if na != 0 {
			which = 1
		}
		return 0, Stats{}, newError(KindZeroNorm, which, -1,
			"cosine is undefined for a zero-norm vector")
	}
	if math.IsInf(na, 0) || math.IsInf(nb, 0) {
		return 0, Stats{}, newError(KindNonFinite, -1, -1,
			"squared norm is not finite")
	}
	dot, steps := mergeDot(a, b)
	if math.IsNaN(dot) || math.IsInf(dot, 0) {
		return 0, Stats{}, newError(KindNonFinite, -1, -1,
			"dot product is not finite")
	}
	if identical(a, b) {
		return 1, Stats{Steps: steps, ExplicitZeros: zeros}, nil
	}
	cos := dot / math.Sqrt(na) / math.Sqrt(nb)
	if cos > 1 {
		cos = 1
	} else if cos < -1 {
		cos = -1
	}
	return cos, Stats{Steps: steps, ExplicitZeros: zeros}, nil
}

// selfNorm returns the squared L2 norm, accumulated with the same
// compensated summation as the merge. A single pass, no merge needed.
func selfNorm(v Vector) float64 {
	var sum, comp float64
	for _, val := range v.Values {
		if val == 0 {
			continue
		}
		sum, comp = neumaierAdd(sum, comp, val*val)
	}
	return sum + comp
}

// identical reports elementwise bitwise equality of the two vectors.
func identical(a, b Vector) bool {
	if len(a.Indices) != len(b.Indices) {
		return false
	}
	for i := range a.Indices {
		if a.Indices[i] != b.Indices[i] ||
			math.Float64bits(a.Values[i]) != math.Float64bits(b.Values[i]) {
			return false
		}
	}
	return true
}
