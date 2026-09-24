package bits

import (
	"math"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := []struct {
		name       string
		x          float64
		sign       uint
		mant       uint64
		exp        int
		zero       bool
		finite     bool
		ratNumIsM2 bool
	}{
		{"one", 1, 0, 1 << 52, -52, false, true, false},
		{"two", 2, 0, 1 << 52, -51, false, true, false},
		{"minus-half", -0.5, 1, 1 << 52, -53, false, true, false},
		{"plus-zero", 0, 0, 0, 0, true, true, false},
		{"minus-zero", math.Copysign(0, -1), 1, 0, 0, true, true, false},
		{"smallest-subnormal", math.SmallestNonzeroFloat64, 0, 1, -1074, false, true, false},
		{"max", math.MaxFloat64, 0, 0, 0, false, true, false},
		{"nan", math.NaN(), 0, 0, 0, false, false, false},
		{"pinf", math.Inf(1), 0, 0, 0, false, false, false},
		{"ninf", math.Inf(-1), 1, 0, 0, false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsFinite(c.x); got != c.finite {
				t.Fatalf("IsFinite=%v want %v", got, c.finite)
			}
			if !c.finite {
				return
			}
			p := Split(c.x)
			if p.Sign != c.sign {
				t.Errorf("sign=%d want %d", p.Sign, c.sign)
			}
			if p.Zero != c.zero {
				t.Errorf("zero=%v want %v", p.Zero, c.zero)
			}
			if c.name == "max" {
				if p.Mantissa.BitLen() != 53 || p.Exp != 971 {
					t.Errorf("max decomp m bits=%d exp=%d", p.Mantissa.BitLen(), p.Exp)
				}
				return
			}
			if !p.Zero && p.Mantissa.Uint64() != c.mant {
				t.Errorf("mantissa=%d want %d", p.Mantissa.Uint64(), c.mant)
			}
			if !p.Zero && p.Exp != c.exp {
				t.Errorf("exp=%d want %d", p.Exp, c.exp)
			}
		})
	}
}

func TestRat(t *testing.T) {
	cases := []struct {
		x     float64
		n, d  int64
	}{
		{1, 1, 1},
		{0.5, 1, 2},
		{0.25, 1, 4},
		{3, 3, 1},
		{math.SmallestNonzeroFloat64, 1, 0}, // denominator huge: check bit length
	}
	for i, c := range cases {
		p := Split(c.x)
		n, d := Rat(p)
		if i == len(cases)-1 {
			if n.Int64() != 1 || d.BitLen() != 1075 {
				t.Fatalf("subnormal rat n=%v d bits=%d", n, d.BitLen())
			}
			continue
		}
		if n.Int64() != c.n || d.Int64() != c.d {
			t.Fatalf("Rat(%v)=%v/%v want %d/%d", c.x, n, d, c.n, c.d)
		}
	}
}
