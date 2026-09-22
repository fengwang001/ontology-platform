package ontology

import "math"

// Element is one non-zero (or explicitly zero) entry of a sparse vector.
type Element struct {
	Index uint32
	Value float64
}

// SparseVector is a sparse vector as (index, value) elements sorted by
// strictly ascending Index. Explicit zero values are legal.
type SparseVector []Element

// Stats reports observability counters for one computation.
type Stats struct {
	// Steps is the number of merge iterations the two-pointer scan
	// advanced. It is bounded by len(a)+len(b), never by the index
	// space, so billion-scale indexes still cost single-digit steps.
	Steps int
	// ExplicitZeros counts elements whose stored value is exactly 0.
	// They are legal, are counted here, and do not affect the result.
	ExplicitZeros int
}

// validate checks that v is strictly ascending by index and contains
// no NaN or infinite values, counting explicit zeros into st.
func validate(v SparseVector, side Side, st *Stats) error {
	var prev uint32
	for i, e := range v {
		if math.IsNaN(e.Value) {
			return &NaNError{Side: side, Pos: i}
		}
		if math.IsInf(e.Value, 0) {
			return &InfError{Side: side, Pos: i, Value: e.Value}
		}
		if i > 0 && e.Index <= prev {
			return &OrderError{Side: side, Pos: i, Prev: prev, Got: e.Index}
		}
		if e.Value == 0 {
			st.ExplicitZeros++
		}
		prev = e.Index
	}
	return nil
}

// sumSquares returns the compensated (Neumaier) sum of squared values.
func sumSquares(v SparseVector) float64 {
	sum, comp := 0.0, 0.0
	for _, e := range v {
		sum, comp = neumaier(sum, comp, e.Value*e.Value)
	}
	return sum + comp
}

// neumaier folds one term into a running compensated sum.
// Using Neumaier (improved Kahan) compensation keeps the result
// accurate when term magnitudes differ wildly (e.g. 1e16 mixed with 1)
// and, being a pure function of the merged term sequence, makes the
// result depend only on index order — never on input arrival order.
func neumaier(sum, comp, term float64) (float64, float64) {
	t := sum + term
	if math.Abs(sum) >= math.Abs(term) {
		comp += (sum - t) + term
	} else {
		comp += (term - t) + sum
	}
	return t, comp
}
