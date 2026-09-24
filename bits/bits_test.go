package bits

import (
	"math"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := []struct {
		name string
		f    float64
		want Parts
	}{
		{"one", 1.0, Parts{false, 1023, 0}},
		{"neg two", -2.0, Parts{true, 1024, 0}},
		{"pos zero", 0.0, Parts{false, 0, 0}},
		{"neg zero", math.Copysign(0, -1), Parts{true, 0, 0}},
		{"max", math.MaxFloat64, Parts{false, 2046, 1<<52 - 1}},
		{"smallest subnormal", math.SmallestNonzeroFloat64, Parts{false, 0, 1}},
		{"inf", math.Inf(1), Parts{false, 0x7ff, 0}},
		{"neg inf", math.Inf(-1), Parts{true, 0x7ff, 0}},
	}
	for _, c := range cases {
		if got := Split(c.f); got != c.want {
			t.Errorf("%s: Split(%v) = %+v, want %+v", c.name, c.f, got, c.want)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name           string
		f              float64
		nan, inf, zero bool
	}{
		{"nan", math.NaN(), true, false, false},
		{"inf", math.Inf(1), false, true, false},
		{"neg inf", math.Inf(-1), false, true, false},
		{"pos zero", 0, false, false, true},
		{"neg zero", math.Copysign(0, -1), false, false, true},
		{"one", 1, false, false, false},
		{"subnormal", math.SmallestNonzeroFloat64, false, false, false},
	}
	for _, c := range cases {
		p := Split(c.f)
		if p.IsNaN() != c.nan || p.IsInf() != c.inf || p.IsZero() != c.zero {
			t.Errorf("%s: got nan=%v inf=%v zero=%v", c.name, p.IsNaN(), p.IsInf(), p.IsZero())
		}
	}
}

func TestIntValue(t *testing.T) {
	cases := []struct {
		name   string
		f      float64
		wantM  uint64
		wantE2 int
	}{
		{"one", 1.0, 1 << 52, -1075 + 1023},
		{"max", math.MaxFloat64, 1<<53 - 1, 2046 - 1075},
		{"smallest subnormal", math.SmallestNonzeroFloat64, 1, -1074},
		{"largest subnormal", math.Float64frombits(0x000FFFFFFFFFFFFF), 1<<52 - 1, -1074},
		{"zero", 0, 0, -1074},
		{"2^53", 1 << 53, 1 << 52, 1},
	}
	for _, c := range cases {
		m, e2 := Split(c.f).IntValue()
		if m != c.wantM || e2 != c.wantE2 {
			t.Errorf("%s: IntValue = (%d,%d), want (%d,%d)", c.name, m, e2, c.wantM, c.wantE2)
		}
	}
}
