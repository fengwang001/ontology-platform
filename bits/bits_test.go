package bits

import (
	"math"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := []struct {
		name string
		x    float64
		neg  bool
		kind Kind
		mant uint64
		exp  int
	}{
		{"+0", 0, false, KindZero, 0, 0},
		{"-0", math.Copysign(0, -1), true, KindZero, 0, 0},
		{"+Inf", math.Inf(1), false, KindInf, 0, 0},
		{"-Inf", math.Inf(-1), true, KindInf, 0, 0},
		{"NaN", math.NaN(), false, KindNaN, 0, 0},
		{"one", 1, false, KindNormal, 1 << 52, -52},
		{"half", 0.5, false, KindNormal, 1 << 52, -53},
		{"two", 2, false, KindNormal, 1 << 52, -51},
		{"min normal", math.Float64frombits(1 << 52), false, KindNormal, 1 << 52, -1074},
		{"smallest nonzero", math.SmallestNonzeroFloat64, false, KindSubnormal, 1, -1074},
		{"max", math.MaxFloat64, false, KindNormal, (1<<52-1)|1<<52, 971},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Split(c.x)
			if p.Neg != c.neg || p.Kind != c.kind || p.Mant != c.mant || (p.Kind == KindNormal || p.Kind == KindSubnormal) && p.Exp != c.exp {
				t.Fatalf("Split(%v): neg=%v kind=%v mant=%d exp=%d, want neg=%v kind=%v mant=%d exp=%d",
					c.x, p.Neg, p.Kind, p.Mant, p.Exp, c.neg, c.kind, c.mant, c.exp)
			}
		})
	}
}

func TestSplitBitsRoundTrip(t *testing.T) {
	for _, x := range []float64{0, 1, -1, 0.1, 1.0 / 3, math.MaxFloat64, math.SmallestNonzeroFloat64} {
		if got := Split(x).Bits; got != math.Float64bits(x) {
			t.Fatalf("bits mismatch for %v", x)
		}
	}
}
