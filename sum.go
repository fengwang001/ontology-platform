package ontology

import (
	"math"
	"math/big"
)

// groupSum accumulates the Sum aggregate of one group.
//
// Order independence: every finite int64/float64 value is converted
// exactly to a big.Rat (a float64 is an exact dyadic rational, an
// int64 an exact integer) and added with exact rational arithmetic.
// Rational addition is commutative and associative with no rounding,
// so the accumulated rational is identical for any arrival order of
// the same multiset of rows. The single rounding to float64 happens
// once, at read time, via big.Rat.Float64 (round to nearest even),
// which is a pure function of that rational. Hence math.Float64bits
// of the result is bit-identical across all input permutations,
// unlike left-to-right float64 accumulation where each addition
// rounds and the rounding depends on order.
//
// Infinities cannot be represented as rationals, so they are counted
// separately and folded in at read time with IEEE 754 semantics.
// NaN inputs are skipped (counted in skipped) and never poison a group.
type groupSum struct {
	rat      *big.Rat // exact sum of all finite values
	sawFloat bool     // any float64 value (incl. infinities) seen
	posInf   int64    // count of +Inf values
	negInf   int64    // count of -Inf values
	skipped  int64    // rows not summed: missing attr, wrong type, NaN
}

func newGroupSum() *groupSum {
	return &groupSum{rat: new(big.Rat)}
}

// addValue folds the row's sum-attribute value into the accumulator.
// Only int64 and float64 are summable; anything else (including a
// missing attribute) is skipped and counted.
func (s *groupSum) addValue(v any, present bool) {
	if !present {
		s.skipped++
		return
	}
	switch n := v.(type) {
	case int64:
		s.rat.Add(s.rat, new(big.Rat).SetInt64(n))
	case float64:
		s.addFloat(n)
	default:
		s.skipped++
	}
}

func (s *groupSum) addFloat(f float64) {
	switch {
	case math.IsNaN(f):
		// Skipped NaN does not make the group "mixed".
		s.skipped++
	case math.IsInf(f, 1):
		s.sawFloat = true
		s.posInf++
	case math.IsInf(f, -1):
		s.sawFloat = true
		s.negInf++
	default:
		s.sawFloat = true
		// SetFloat64 is exact for finite f (documented); a nil
		// return would mean non-finite, which is excluded above.
		s.rat.Add(s.rat, new(big.Rat).SetFloat64(f))
	}
}

// sumResult is the read-out of one group's Sum.
type sumResult struct {
	isInt    bool    // true when only int64 values were summed
	intSum   int64   // valid when isInt && !overflow
	floatSum float64 // valid when !isInt
	overflow bool    // all-int64 group whose sum exceeds int64
}

// result computes the final sum. It is deterministic for a fixed
// accumulated state, hence order-independent across runs.
func (s *groupSum) result() sumResult {
	if !s.sawFloat {
		// Only int64 values were added, so the rational is an integer.
		num := s.rat.Num()
		if num.IsInt64() {
			return sumResult{isInt: true, intSum: num.Int64()}
		}
		// Exact integer sum exceeds int64: report a decidable
		// overflow instead of wrapping or degrading to float64.
		return sumResult{isInt: true, overflow: true}
	}
	switch {
	case s.posInf > 0 && s.negInf > 0:
		return sumResult{floatSum: math.NaN()} // IEEE: +Inf + -Inf
	case s.posInf > 0:
		return sumResult{floatSum: math.Inf(1)}
	case s.negInf > 0:
		return sumResult{floatSum: math.Inf(-1)}
	}
	f, _ := s.rat.Float64()
	return sumResult{floatSum: f}
}
