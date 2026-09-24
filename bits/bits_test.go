package bits

import (
	"math"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := []struct {
		name string
		x    float64
		kind Kind
		neg  bool
		exp  int
		mant uint64
	}{
		{"pos zero", 0, Zero, false, 0, 0},
		{"neg zero", math.Copysign(0, -1), Zero, true, 0, 0},
		{"one", 1, Normal, false, -52, 1 << 52},
		{"minus one", -1, Normal, true, -52, 1 << 52},
		{"two", 2, Normal, false, -51, 1 << 52},
		{"1.5", 1.5, Normal, false, -52, 1<<52 | 1<<51},
		{"max", math.MaxFloat64, Normal, false, 971, 1<<53 - 1},
		{"smallest sub", math.SmallestNonzeroFloat64, Subnormal, false, -1074, 1},
		{"max sub", math.Float64frombits(0x000fffffffffffff), Subnormal, false, -1074, 1<<52 - 1},
		{"inf", math.Inf(1), Inf, false, 0, 0},
		{"-inf", math.Inf(-1), Inf, true, 0, 0},
		{"nan", math.NaN(), NaN, false, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Split(c.x)
			if p.Kind != c.kind || p.Neg != c.neg || p.Exp != c.exp || (p.Kind != NaN && p.Mant != c.mant) {
				t.Fatalf("Split(%v) = %+v, want kind=%v neg=%v exp=%d mant=%d", c.x, p, c.kind, c.neg, c.exp, c.mant)
			}
		})
	}
}

func TestJoinRoundTrip(t *testing.T) {
	xs := []float64{0, 1, -1, 2, 1.5, -3.25, 1e308, math.MaxFloat64, math.SmallestNonzeroFloat64,
		math.Float64frombits(0x000fffffffffffff), math.Float64frombits(0x0008000000000001)}
	for _, x := range xs {
		p := Split(x)
		if p.Kind != Normal && p.Kind != Subnormal {
			continue
		}
		if got := math.Float64frombits(Join(p.Neg, p.Exp, p.Mant)); got != x {
			t.Fatalf("Join(Split(%v)) = %v", x, got)
		}
	}
}

func TestJoinOverflow(t *testing.T) {
	if got := math.Float64frombits(Join(false, 2000, 1)); !math.IsInf(got, 1) {
		t.Fatalf("overflow = %v, want +Inf", got)
	}
}
