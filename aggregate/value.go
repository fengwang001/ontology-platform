package aggregate

import (
	"math"
	"sort"
)

// sumValue is one summable observation. Exactly one mode is active:
// isInt true means the observation was an int64 held in i; otherwise it was
// a float64 held in f.
type sumValue struct {
	i     int64
	f     float64
	isInt bool
}

// classifySum inspects the sum attribute on one row.
// ok is true only when the row contributes to the group's Sum: the attribute
// exists and is either an int64 or a non-NaN float64 (infinities do
// contribute). Missing attributes, nil, unsummable types (string, bool, ...)
// and NaN all return ok=false so the row is counted by Count and by the
// per-group skipped counter without ever poisoning the sum with NaN.
func classifySum(sumAttr string, row map[string]any) (sumValue, bool) {
	v, ok := row[sumAttr]
	if !ok || v == nil {
		return sumValue{}, false
	}
	switch n := v.(type) {
	case int64:
		return sumValue{i: n, isInt: true}, true
	case float64:
		if math.IsNaN(n) {
			return sumValue{}, false
		}
		return sumValue{f: n}, true
	default:
		return sumValue{}, false
	}
}

// addInt64 returns the sum a+b and whether it stayed inside the int64 range.
// The overflow check is done before wrapping, so callers can reject overflow
// explicitly instead of silently relying on two's-complement wraparound.
func addInt64(a, b int64) (int64, bool) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, false
	}
	return a + b, true
}

// orderIndependentSum reduces a multiset of float64 to one result whose
// IEEE 754 bit pattern is independent of the order the observations arrived
// in.
//
// Why this is order-independent: every input multiset is first put into one
// canonical sequence by sorting (numeric value ascending; equal values such
// as -0.0/+0.0, which compare equal numerically, are tie-broken by their raw
// bits), and only then folded from left to right. Any permutation of the
// same inputs sorts into the identical sequence, and IEEE 754 double
// addition is deterministic, so the final bit pattern is identical. NaN
// inputs are assumed to have been filtered by classifySum already.
func orderIndependentSum(values []float64) float64 {
	// Sort into the canonical order (the slice is a caller-owned copy).
	sort.Slice(values, func(i, j int) bool { return floatLess(values[i], values[j]) })
	var acc float64
	for _, v := range values {
		acc += v
	}
	return acc
}

// floatLess defines the canonical total order used by orderIndependentSum:
// numeric ascending, with raw IEEE 754 bits as the deterministic tie-break
// for values that compare equal (notably +0.0 versus -0.0).
func floatLess(a, b float64) bool {
	if a < b {
		return true
	}
	if a > b {
		return false
	}
	// a == b numerically, including 0.0 == -0.0; break the tie by bits.
	return math.Float64bits(a) < math.Float64bits(b)
}
