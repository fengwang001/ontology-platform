package sparse

import (
	"math"
	"math/rand"
	"testing"
)

// Zero norm (empty or all-explicit-zero vectors) makes cosine undefined:
// a typed error, never NaN or 0.
func TestCosineZeroNormError(t *testing.T) {
	nonzero := Vector{{0, 1}}
	zero := Vector{{0, 0}, {7, 0}}
	cases := []struct {
		a, b Vector
		vec  string
	}{
		{Vector{}, nonzero, "left"},
		{nonzero, Vector{}, "right"},
		{zero, nonzero, "left"},
		{nonzero, zero, "right"},
		{Vector{}, Vector{}, "left"},
	}
	for _, c := range cases {
		got, _, err := Cosine(c.a, c.b)
		if !isError(err, ErrZeroNorm, c.vec, -1) {
			t.Fatalf("want ErrZeroNorm(%s), got %v (%v)", c.vec, got, err)
		}
		if math.IsNaN(got) {
			t.Fatal("returned NaN alongside error")
		}
	}
}

// Identical vectors must give exactly 1.0, bit for bit — no
// 1.0000000000000002 from sqrt/divide rounding.
func TestCosineIdenticalIsExactlyOne(t *testing.T) {
	vectors := []Vector{
		{{0, 0.1}, {1, 0.2}, {2, 1.0 / 3.0}, {9, math.Pi}},
		{{5, 1e16}, {6, 1}, {7, -1e16}},
		{{0, 7}},
		{{0, 1}, {1, 0}, {2, -3}}, // explicit zero kept
	}
	for i, v := range vectors {
		got, _, err := Cosine(v, v)
		if err != nil {
			t.Fatal(err)
		}
		if math.Float64bits(got) != math.Float64bits(1.0) {
			t.Fatalf("vector %d: cosine bits %x, want exactly 1", i,
				math.Float64bits(got))
		}
	}
}

// Cosine of any valid pair stays within [-1, 1].
func TestCosineClampedToUnitInterval(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for trial := 0; trial < 500; trial++ {
		a := randVector(rng, 32)
		b := randVector(rng, 32)
		c, _, err := Cosine(a, b)
		if err != nil {
			t.Fatal(err)
		}
		if c < -1 || c > 1 {
			t.Fatalf("trial %d: cosine %v outside [-1,1]", trial, c)
		}
	}
	// Antiparallel unit vectors give exactly -1.
	a := Vector{{0, 1}}
	b := Vector{{0, -1}}
	if c, _, err := Cosine(a, b); err != nil || c != -1 {
		t.Fatalf("antiparallel: got %v, %v", c, err)
	}
}

// A known value: cos((1,0),(1,1)) = 1/sqrt(2).
func TestCosineKnownValue(t *testing.T) {
	a := Vector{{0, 1}}
	b := Vector{{0, 1}, {1, 1}}
	got, _, err := Cosine(a, b)
	if err != nil {
		t.Fatal(err)
	}
	// NB: 1/math.Sqrt2 as a constant would be rounded once from the exact
	// value; the runtime path divides by the already-rounded sqrt(2).
	if want := 1 / math.Sqrt(2); got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// Inf components make norms non-finite: typed error, not NaN.
func TestCosineNonFiniteError(t *testing.T) {
	a := Vector{{0, math.Inf(1)}}
	b := Vector{{0, 1}}
	if _, _, err := Cosine(a, b); !isError(err, ErrNonFinite, "", -1) {
		t.Fatalf("left Inf: got %v", err)
	}
	if _, _, err := Cosine(b, a); !isError(err, ErrNonFinite, "", -1) {
		t.Fatalf("right Inf: got %v", err)
	}
}

func randVector(rng *rand.Rand, space int) Vector {
	n := rng.Intn(space/2) + 1
	v := make(Vector, 0, n)
	for _, k := range rng.Perm(space)[:n] {
		v = append(v, Element{uint32(k), rng.NormFloat64()})
	}
	sortV(v)
	return v
}
