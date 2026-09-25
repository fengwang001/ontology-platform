package sparse

import "math"

// Dot computes the dot product of two sparse vectors with a single
// two-pointer merge. Terms are accumulated with Neumaier compensated
// summation, so the result stays accurate when term magnitudes differ
// wildly (e.g. 1e16 alongside 1). The merge order is fully determined
// by the sorted indices, so repeated calls are bit-for-bit identical.
// Neither input is modified.
func Dot(a, b *Vector) (float64, Stats, error) {
	var stats Stats
	if err := validate(a, 0, &stats); err != nil {
		return 0, stats, err
	}
	if err := validate(b, 1, &stats); err != nil {
		return 0, stats, err
	}
	sum := mergeDot(a.Elems, b.Elems, &stats)
	if math.IsNaN(sum) || math.IsInf(sum, 0) {
		return 0, stats, &NumericError{Op: "dot", Detail: "result is not finite"}
	}
	return sum, stats, nil
}

// mergeDot performs the two-pointer merge over two validated element
// slices, accumulating matching-index products with Neumaier
// compensation. It assumes both slices are sorted by Index.
func mergeDot(a, b []Element, stats *Stats) float64 {
	var sum, comp float64
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		stats.Steps++
		switch {
		case a[i].Index < b[j].Index:
			i++
		case a[i].Index > b[j].Index:
			j++
		default:
			sum, comp = addCompensated(sum, comp, a[i].Value*b[j].Value)
			i++
			j++
		}
	}
	return sum + comp
}

// addCompensated adds term to sum using Neumaier's variant of Kahan
// summation, returning the new sum and compensation.
func addCompensated(sum, comp, term float64) (float64, float64) {
	t := sum + term
	if math.Abs(sum) >= math.Abs(term) {
		comp += (sum - t) + term
	} else {
		comp += (term - t) + sum
	}
	return t, comp
}
