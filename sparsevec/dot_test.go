package sparsevec

import (
	"math"
	"math/rand"
	"testing"
)

// Billion-scale indices must be handled by a single-digit number of merge
// steps, proving nothing is expanded into dense form.
func TestDotBillionIndexSteps(t *testing.T) {
	a := Vector{
		{Index: 0, Value: 1},
		{Index: 500_000_000, Value: 2},
		{Index: 1_000_000_000, Value: 3},
	}
	b := Vector{
		{Index: 1, Value: 4},
		{Index: 500_000_000, Value: 5},
		{Index: 1_000_000_000, Value: 6},
	}
	dot, st, err := Dot(a, b)
	if err != nil {
		t.Fatalf("Dot: %v", err)
	}
	if st.Steps >= 10 {
		t.Fatalf("steps = %d, want single-digit merge steps", st.Steps)
	}
	if want := 2*5.0 + 3*6.0; dot != want {
		t.Fatalf("dot = %v, want %v", dot, want)
	}
}

// Small-scale cross-check against a naive dense expansion: exact equality.
func TestDotMatchesNaiveExpansion(t *testing.T) {
	a := Vector{{0, 1.5}, {3, -2}, {7, 4}}
	b := Vector{{1, 8}, {3, 0.25}, {7, -1}}
	dot, _, err := Dot(a, b)
	if err != nil {
		t.Fatalf("Dot: %v", err)
	}
	if want := naiveDot(a, b); dot != want {
		t.Fatalf("dot = %v, naive = %v", dot, want)
	}
}

// Shuffling elements and re-sorting must not change a single bit: the
// summation order is fixed by indices, not by input order.
func TestDotOrderIndependentBitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	base := make([]Element, 64)
	other := make([]Element, 64)
	for i := range base {
		base[i] = Element{Index: uint32(i * 3), Value: rng.NormFloat64() * 1e8}
		other[i] = Element{Index: uint32(i*3 + 1), Value: rng.NormFloat64()}
	}
	a := sortedCopy(base)
	b := sortedCopy(other)
	ref, _, err := Dot(a, b)
	if err != nil {
		t.Fatalf("Dot: %v", err)
	}
	for trial := 0; trial < 50; trial++ {
		rng.Shuffle(len(base), func(i, j int) { base[i], base[j] = base[j], base[i] })
		rng.Shuffle(len(other), func(i, j int) { other[i], other[j] = other[j], other[i] })
		got, _, err := Dot(sortedCopy(base), sortedCopy(other))
		if err != nil {
			t.Fatalf("Dot: %v", err)
		}
		if math.Float64bits(got) != math.Float64bits(ref) {
			t.Fatalf("trial %d: bits differ: %x vs %x", trial,
				math.Float64bits(got), math.Float64bits(ref))
		}
	}
}

// With wildly different magnitudes (1e16 vs 1) the compensated sum must
// stay within 1e-15 relative error of a 256-bit big.Float reference.
func TestDotCompensatedAgainstBigRef(t *testing.T) {
	a := Vector{{0, 1e16}, {1, 1}, {2, -1e16}}
	b := Vector{{0, 1}, {1, 1}, {2, 1}}
	dot, _, err := Dot(a, b)
	if err != nil {
		t.Fatalf("Dot: %v", err)
	}
	ref := bigDot(a, b)
	if got := relErr(dot, ref); got > 1e-15 {
		t.Fatalf("relative error = %v, want <= 1e-15 (dot=%v)", got, dot)
	}
	// The exact answer is 1; a plain left-to-right float64 sum gives 0.
	if dot != 1 {
		t.Fatalf("dot = %v, want exactly 1", dot)
	}
}

// Repeated computation of the same pair must be bit-identical.
func TestDotRepeatable(t *testing.T) {
	a := Vector{{0, 0.1}, {2, 0.3}, {9, -0.7}}
	b := Vector{{2, 1.1}, {9, 0.9}}
	first, _, err := Dot(a, b)
	if err != nil {
		t.Fatalf("Dot: %v", err)
	}
	for i := 0; i < 100; i++ {
		got, _, err := Dot(a, b)
		if err != nil {
			t.Fatalf("Dot: %v", err)
		}
		if math.Float64bits(got) != math.Float64bits(first) {
			t.Fatalf("run %d: bits differ", i)
		}
	}
}
