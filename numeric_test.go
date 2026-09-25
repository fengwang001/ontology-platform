package sparse

import (
	"math"
	"math/big"
	"testing"
)

// TestDotCompensatedAgainstBigFloat checks that Neumaier compensated
// summation keeps the dot product within 1e-15 relative error of a
// 256-bit math/big reference when term magnitudes differ by 1e16.
func TestDotCompensatedAgainstBigFloat(t *testing.T) {
	a := &Vector{Elems: []Element{
		{Index: 0, Value: 1e16},
		{Index: 1, Value: 1},
		{Index: 2, Value: -1e16},
		{Index: 3, Value: 1},
	}}
	b := &Vector{Elems: []Element{
		{Index: 0, Value: 1},
		{Index: 1, Value: 1},
		{Index: 2, Value: 1},
		{Index: 3, Value: 1},
	}}
	got, _, err := Dot(a, b)
	if err != nil {
		t.Fatalf("Dot: %v", err)
	}
	ref := bigFloatDot(a, b)
	ref64, _ := ref.Float64()
	if relErr(got, ref64) > 1e-15 {
		t.Fatalf("relative error too large: got %v ref %v", got, ref64)
	}
	// Sanity: naive left-to-right summation would give 0 here.
	if got != 2 {
		t.Fatalf("compensated dot = %v, want exact 2", got)
	}
}

func bigFloatDot(a, b *Vector) *big.Float {
	vals := map[uint32]*big.Float{}
	for _, e := range a.Elems {
		vals[e.Index] = new(big.Float).SetPrec(256).SetFloat64(e.Value)
	}
	total := new(big.Float).SetPrec(256)
	for _, e := range b.Elems {
		if av, ok := vals[e.Index]; ok {
			bv := new(big.Float).SetPrec(256).SetFloat64(e.Value)
			total.Add(total, new(big.Float).SetPrec(256).Mul(av, bv))
		}
	}
	return total
}

func relErr(got, ref float64) float64 {
	if ref == 0 {
		return math.Abs(got)
	}
	return math.Abs(got-ref) / math.Abs(ref)
}
