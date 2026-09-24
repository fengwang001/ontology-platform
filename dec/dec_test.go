package dec

import (
	"math"
	"math/big"
	"testing"

	"ontology/bits"
)

// exactParse rebuilds a float64 from digits and scientific exponent with
// exact rational arithmetic (reference for assertions).
func exactParse(t *testing.T, digits string, exp10 int, neg bool) float64 {
	t.Helper()
	d, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		t.Fatalf("bad digits %q", digits)
	}
	e := exp10 - len(digits) + 1
	r := new(big.Rat).SetInt(d)
	p := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(abs(e))), nil)
	if e >= 0 {
		r.Mul(r, new(big.Rat).SetInt(p))
	} else {
		r.Quo(r, new(big.Rat).SetInt(p))
	}
	f, _ := r.Float64()
	if neg {
		f = -f
	}
	return f
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func shortestOf(f float64) (string, int) {
	m, e2 := bits.Split(f).IntValue()
	return Shortest(m, e2)
}

func TestShortest(t *testing.T) {
	cases := []struct {
		name   string
		f      float64
		digits string
		exp10  int
		maxK   int
	}{
		{"0.1", 0.1, "1", -1, 1},
		{"1/3", 1.0 / 3.0, "3333333333333333", -1, 16},
		{"one", 1.0, "1", 0, 1},
		{"0.5", 0.5, "5", -1, 1},
		{"max", math.MaxFloat64, "17976931348623157", 308, 17},
		{"smallest subnormal", math.SmallestNonzeroFloat64, "5", -324, 1},
		{"2^53", 1 << 53, "9007199254740992", 15, 16},
		{"2^53+2", 1<<53 + 2, "9007199254740994", 15, 16},
		{"trap16", math.Float64frombits(0x3c04951aa42655d9), "13947202335788668", -19, 17},
	}
	for _, c := range cases {
		digits, exp10 := shortestOf(c.f)
		if digits != c.digits || exp10 != c.exp10 {
			t.Errorf("%s: got (%s,%d), want (%s,%d)", c.name, digits, exp10, c.digits, c.exp10)
		}
		if len(digits) > c.maxK {
			t.Errorf("%s: %d digits, want <= %d", c.name, len(digits), c.maxK)
		}
		neg := math.Signbit(c.f)
		if got := exactParse(t, digits, exp10, neg); math.Float64bits(got) != math.Float64bits(c.f) {
			t.Errorf("%s: round-trip bits differ", c.name)
		}
	}
}

// TestFloatBacksubstitutionTrap documents a 16-digit decimal that a naive
// float64 Horner parse maps back to x, while exact comparison lands one
// ulp away; the correct shortest answer is 17 digits.
func TestFloatBacksubstitutionTrap(t *testing.T) {
	x := math.Float64frombits(0x3c04951aa42655d9) // 1.3947202335788668e-19
	naive := 0.0
	for _, c := range "1394720233578867" {
		naive = naive*10 + float64(c-'0')
	}
	naive *= math.Pow(10, -34)
	if naive != x {
		t.Fatal("precondition: naive float parse should look equal")
	}
	exact := exactParse(t, "1394720233578867", -19, false)
	if math.Float64bits(exact) == math.Float64bits(x) {
		t.Fatal("precondition: exact parse must differ by one ulp")
	}
	digits, _ := shortestOf(x)
	if len(digits) != 17 {
		t.Errorf("got %d digits (%s), want 17", len(digits), digits)
	}
}

func TestCheckCount(t *testing.T) {
	ResetChecks()
	shortestOf(0.1)
	if n := Checks(); n > 3 {
		t.Errorf("0.1 used %d exact round-trip checks, want <= 3", n)
	}
}

func TestSubnormalSample(t *testing.T) {
	for _, b := range []uint64{1, 2, 3, 1 << 20, 1<<51 + 1, 1<<52 - 1} {
		f := math.Float64frombits(b)
		digits, exp10 := shortestOf(f)
		if got := exactParse(t, digits, exp10, false); got != f {
			t.Errorf("subnormal bits %#x: round-trip failed with %s e%d", b, digits, exp10)
		}
	}
}
