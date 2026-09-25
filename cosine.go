package sparse

import "math"

// normSq returns the compensated sum of squared values of v.
func normSq(v Vector) float64 {
	sum, comp := 0.0, 0.0
	for _, e := range v {
		sum, comp = addComp(sum, comp, e.Value*e.Value)
	}
	return sum + comp
}

// Cosine returns the cosine similarity of a and b. It reuses the same
// single-merge dot product and reports combined Stats.
//
// Guarantees:
//   - zero-norm input (empty or all-zero vector) -> *Error, ErrZeroNorm;
//   - bitwise-identical vectors -> exactly 1.0, no rounding drift;
//   - the result is clamped to [-1, 1];
//   - a non-finite intermediate (Inf inputs) -> *Error, ErrNonFinite,
//     never a bare NaN.
func Cosine(a, b Vector) (float64, Stats, error) {
	dot, stats, err := Dot(a, b)
	if err != nil {
		return 0, stats, err
	}
	na, nb := normSq(a), normSq(b)
	if math.IsInf(na, 0) || math.IsNaN(na) {
		return 0, stats, nonFiniteErr("left vector squared norm", na)
	}
	if math.IsInf(nb, 0) || math.IsNaN(nb) {
		return 0, stats, nonFiniteErr("right vector squared norm", nb)
	}
	if na == 0 {
		return 0, stats, zeroNormErr("left")
	}
	if nb == 0 {
		return 0, stats, zeroNormErr("right")
	}
	// Identical vectors have cosine exactly 1 by definition; returning the
	// literal avoids sqrt/divide rounding that could yield 1.0000000000000002.
	if identical(a, b) {
		return 1, stats, nil
	}
	c := dot / (math.Sqrt(na) * math.Sqrt(nb))
	if math.IsNaN(c) || math.IsInf(c, 0) {
		return 0, stats, nonFiniteErr("cosine similarity", c)
	}
	return min(1, max(-1, c)), stats, nil
}
