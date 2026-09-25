package sparse

import "math"

// Cosine computes the cosine similarity of two sparse vectors via one
// merge pass each for the dot product and the two squared norms.
//
// Boundary behavior:
//   - a zero-norm vector (empty or all-zero) yields *ZeroNormError;
//   - non-finite values (from infinite inputs) yield *NumericError;
//   - identical vectors return exactly 1, never 1.0000000000000002;
//   - the result is always clamped to [-1, 1].
func Cosine(a, b *Vector) (float64, Stats, error) {
	var stats Stats
	if err := validate(a, 0, &stats); err != nil {
		return 0, stats, err
	}
	if err := validate(b, 1, &stats); err != nil {
		return 0, stats, err
	}
	dot := mergeDot(a.Elems, b.Elems, &stats)
	normA := mergeDot(a.Elems, a.Elems, &stats)
	normB := mergeDot(b.Elems, b.Elems, &stats)
	if normA == 0 {
		return 0, stats, &ZeroNormError{Vector: 0}
	}
	if normB == 0 {
		return 0, stats, &ZeroNormError{Vector: 1}
	}
	for _, v := range []float64{dot, normA, normB} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, stats, &NumericError{Op: "cosine", Detail: "non-finite intermediate result"}
		}
	}
	// Divide by each norm separately to avoid normA*normB overflow.
	cos := dot / math.Sqrt(normA) / math.Sqrt(normB)
	// Identical vectors produce bit-identical sums; force exact 1.
	if dot == normA && dot == normB {
		cos = 1
	}
	cos = math.Max(-1, math.Min(1, cos))
	if math.IsNaN(cos) {
		return 0, stats, &NumericError{Op: "cosine", Detail: "result is NaN"}
	}
	return cos, stats, nil
}
