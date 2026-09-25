package sparse

import (
	"math"
	"math/rand"
	"testing"
)

// Indices reach 1e9 but each vector has only 3 nonzeros: the merge must
// advance a single-digit number of steps, never walk the index space.
func TestDotBillionIndexStepsAreSingleDigit(t *testing.T) {
	a := Vector{{0, 1}, {500_000_000, 2}, {1_000_000_000, 3}}
	b := Vector{{1, 1}, {500_000_000, 4}, {1_000_000_000, 5}}
	dot, st, err := Dot(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if st.Steps >= 10 {
		t.Fatalf("merge took %d steps, expected single digit", st.Steps)
	}
	if dot != 23 {
		t.Fatalf("dot = %v, want 23", dot)
	}
	if got := naiveDotMap(a, b); math.Float64bits(got) != math.Float64bits(dot) {
		t.Fatalf("dot %v != naive expansion %v", dot, got)
	}
}

// Exact bitwise agreement with naive dense expansion on small vectors.
// Integer-valued elements keep every partial sum exact in float64.
func TestDotMatchesNaiveExpansion(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		idxA := rng.Perm(64)[:rng.Intn(30)+1]
		idxB := rng.Perm(64)[:rng.Intn(30)+1]
		a := make(Vector, 0, len(idxA))
		b := make(Vector, 0, len(idxB))
		for _, k := range idxA {
			a = append(a, Element{uint32(k), float64(rng.Intn(2001) - 1000)})
		}
		for _, k := range idxB {
			b = append(b, Element{uint32(k), float64(rng.Intn(2001) - 1000)})
		}
		sortV(a)
		sortV(b)
		got, _, err := Dot(a, b)
		if err != nil {
			t.Fatal(err)
		}
		want := naiveDotMap(a, b)
		if math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("trial %d: got %v want %v", trial, got, want)
		}
	}
}

// Explicit zero elements are legal, counted, and never affect the result.
func TestExplicitZerosCountedAndNeutral(t *testing.T) {
	a0 := Vector{{0, 1}, {1, 0}, {2, 2}}
	b0 := Vector{{0, 1}, {2, 3}, {5, 0}}
	dot, st, err := Dot(a0, b0)
	if err != nil {
		t.Fatal(err)
	}
	if st.ExplicitZeros != 2 {
		t.Fatalf("ExplicitZeros = %d, want 2", st.ExplicitZeros)
	}
	if dot != 7 {
		t.Fatalf("dot = %v, want 7", dot)
	}
	a1 := Vector{{0, 1}, {2, 2}}
	b1 := Vector{{0, 1}, {2, 3}}
	dot1, st1, _ := Dot(a1, b1)
	if dot1 != dot || st1.ExplicitZeros != 0 {
		t.Fatalf("zeros changed result: %v vs %v", dot1, dot)
	}
}

// Shuffling physical order and re-sorting must give a bitwise identical
// result: summation order is fixed by index, not input layout.
func TestDotShuffleResortBitwiseIdentical(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	a := Vector{
		{0, 1e16}, {1, 1}, {2, -1e16}, {3, 3.5},
		{4, -7.25}, {10, 42}, {11, -0.5}, {100, 1e-8},
	}
	b := Vector{
		{0, 1}, {1, 1}, {2, 1}, {3, 1},
		{10, 1}, {11, 1}, {100, 1}, {101, 9},
	}
	want, _, err := Dot(a, b)
	if err != nil {
		t.Fatal(err)
	}
	for trial := 0; trial < 20; trial++ {
		c := append(Vector(nil), a...)
		rng.Shuffle(len(c), func(i, j int) { c[i], c[j] = c[j], c[i] })
		sortV(c)
		got, _, err := Dot(c, b)
		if err != nil {
			t.Fatal(err)
		}
		if math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("trial %d: %v bits != %v bits", trial, got, want)
		}
	}
}

// Magnitude disparity: 1e16 and 1 in the same sum. Neumaier compensation
// recovers cancellation; the result must stay within 1e-15 of math/big.
func TestDotMagnitudeVsBigReference(t *testing.T) {
	a := Vector{{0, 1e16}, {1, 1}, {2, -1e16}, {3, 2.5}, {4, -0.25}}
	b := Vector{{0, 1}, {1, 1}, {2, 1}, {3, 1}, {4, 1}}
	got, _, err := Dot(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if e := relErr(got, bigDot(a, b)); e > 1e-15 {
		t.Fatalf("relative error %g > 1e-15 (got %v)", e, got)
	}
	// Naive uncompensated summation loses the +1 here (1e16+1 rounds to 1e16),
	// proving the compensated path is what keeps us within the bound.
	naive := 1e16 + 1 - 1e16
	if naive == 1 {
		t.Skip("platform unexpectedly kept 1e16+1 exact")
	}
	if got != 3.25 {
		t.Fatalf("compensated dot = %v, want exactly 3.25", got)
	}
}

// ±Inf inputs whose product is Inf or NaN yield a typed error, never a
// raw NaN/Inf handed to the caller.
func TestDotNonFiniteError(t *testing.T) {
	cases := []struct {
		a, b Vector
	}{
		{Vector{{0, math.Inf(1)}}, Vector{{0, 1}}},           // +Inf
		{Vector{{0, math.Inf(-1)}}, Vector{{0, 1}}},          // -Inf
		{Vector{{0, math.Inf(1)}}, Vector{{0, 0}}},           // Inf * 0 = NaN
		{Vector{{0, math.Inf(1)}}, Vector{{0, math.Inf(1)}}}, // Inf*Inf = Inf
	}
	for _, c := range cases {
		got, _, err := Dot(c.a, c.b)
		if !isError(err, ErrNonFinite, "", -1) {
			t.Fatalf("want ErrNonFinite for %v*%v, got %v (%v)",
				c.a[0].Value, c.b[0].Value, got, err)
		}
	}
}

func sortV(v Vector) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j].Index < v[j-1].Index; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}
