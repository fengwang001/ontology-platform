package bits

import (
	"math"
	"testing"
)

func TestDecompose(t *testing.T) {
	cases := []struct {
		x    float64
		neg  bool
		exp  int
		mant uint64
	}{
		{0.0, false, 0, 0},
		{math.Copysign(0, -1), true, 0, 0},
		{1.0, false, 1023, 0},
		{-1.5, true, 1023, 1 << 51},
		{0.1, false, 1019, 0x999999999999a},
		{math.MaxFloat64, false, 2046, 1<<52 - 1},
		{math.SmallestNonzeroFloat64, false, 0, 1},
		{math.Inf(1), false, 2047, 0},
		{math.Inf(-1), true, 2047, 0},
	}
	for _, c := range cases {
		p := Decompose(c.x)
		if p.Neg != c.neg || p.Exp != c.exp || p.Mant != c.mant {
			t.Errorf("Decompose(%v) = %+v, want neg=%v exp=%d mant=%x",
				c.x, p, c.neg, c.exp, c.mant)
		}
		if got := Compose(p); math.Float64bits(got) != math.Float64bits(c.x) {
			t.Errorf("Compose(Decompose(%v)) 不逐位相等", c.x)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		x              float64
		nan, inf, zero bool
	}{
		{math.NaN(), true, false, false},
		{math.Inf(1), false, true, false},
		{math.Inf(-1), false, true, false},
		{0.0, false, false, true},
		{math.Copysign(0, -1), false, false, true},
		{1.0, false, false, false},
		{math.SmallestNonzeroFloat64, false, false, false},
	}
	for _, c := range cases {
		if got := IsNaN(c.x); got != c.nan {
			t.Errorf("IsNaN(%v) = %v, want %v", c.x, got, c.nan)
		}
		if got := IsInf(c.x); got != c.inf {
			t.Errorf("IsInf(%v) = %v, want %v", c.x, got, c.inf)
		}
		if got := IsZero(c.x); got != c.zero {
			t.Errorf("IsZero(%v) = %v, want %v", c.x, got, c.zero)
		}
	}
}

func TestInt(t *testing.T) {
	cases := []struct {
		x   float64
		neg bool
		m   uint64
		e   int
	}{
		{1.0, false, 1 << 52, -52},
		{-2.0, true, 1 << 52, -51},
		{0.1, false, 0x1999999999999a, -56},
		{math.MaxFloat64, false, 1<<53 - 1, 971},
		{math.SmallestNonzeroFloat64, false, 1, -1074},
	}
	for _, c := range cases {
		neg, m, e := Int(c.x)
		if neg != c.neg || m != c.m || e != c.e {
			t.Errorf("Int(%v) = (%v, %x, %d), want (%v, %x, %d)",
				c.x, neg, m, e, c.neg, c.m, c.e)
		}
	}
}
