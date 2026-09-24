package fmtf

import (
	"errors"
	"math"
	"math/big"
	"strings"
	"testing"
)

// exactParse is the test-side exact decimal parser (big.Rat, nearest-even).
func exactParse(t *testing.T, s string) float64 {
	t.Helper()
	if s == "-0" {
		return math.Copysign(0, -1)
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("test text %q not a valid decimal", s)
	}
	f, _ := r.Float64()
	return f
}

func TestEncode(t *testing.T) {
	cases := []struct {
		name string
		f    float64
		want string
	}{
		{"0.1", 0.1, "0.1"},
		{"1/3", 1.0 / 3.0, "0.3333333333333333"},
		{"pos zero", 0, "0"},
		{"neg zero", math.Copysign(0, -1), "-0"},
		{"integer", 12345, "12345"},
		{"negative", -2.5, "-2.5"},
		{"max", math.MaxFloat64, "1.7976931348623157e+308"},
		{"min subnormal", math.SmallestNonzeroFloat64, "5e-324"},
		{"2^53", 1 << 53, "9007199254740992"},
		{"2^53+2", 1<<53 + 2, "9007199254740994"},
		// fixed/scientific boundary: fixed iff -4 <= E < 21
		{"E=20 fixed", 1e20, "100000000000000000000"},
		{"E=20 fixed b", 5e20, "500000000000000000000"},
		{"E=21 sci", 1e21, "1e+21"},
		{"E=21 sci b", 2.5e21, "2.5e+21"},
		{"E=-4 fixed", 1e-4, "0.0001"},
		{"E=-4 fixed b", 2e-4, "0.0002"},
		{"E=-5 sci", 1e-5, "1e-5"},
		{"E=-5 sci b", 3e-5, "3e-5"},
	}
	for _, c := range cases {
		got, err := Encode(c.f)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: Encode = %q, want %q", c.name, got, c.want)
		}
		if rt := exactParse(t, got); math.Float64bits(rt) != math.Float64bits(c.f) {
			t.Errorf("%s: %q does not round-trip", c.name, got)
		}
	}
}

func TestEncodeSpecials(t *testing.T) {
	if _, err := Encode(math.NaN()); !errors.Is(err, ErrNaN) {
		t.Errorf("NaN: got %v, want ErrNaN", err)
	}
	for _, f := range []float64{math.Inf(1), math.Inf(-1)} {
		if _, err := Encode(f); !errors.Is(err, ErrInf) {
			t.Errorf("Inf: got %v, want ErrInf", err)
		}
	}
}

func TestEncodeDeterministic(t *testing.T) {
	first, err := Encode(1.0 / 3.0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		s, err := Encode(1.0 / 3.0)
		if err != nil || s != first {
			t.Fatalf("run %d: %q %v, want %q", i, s, err, first)
		}
	}
	if strings.Contains(first, "e") {
		t.Fatalf("1/3 should use fixed form, got %q", first)
	}
}
