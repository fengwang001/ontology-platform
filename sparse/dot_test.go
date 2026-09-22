package sparse

import (
	"math"
	"slices"
	"testing"
)

// naiveDot expands both vectors into dense maps and sums the
// products in ascending index order: the reference implementation.
func naiveDot(a, b Vector) float64 {
	da := map[uint32]float64{}
	for _, e := range a {
		da[e.Index] = e.Value
	}
	sum := 0.0
	for _, e := range b {
		if v, ok := da[e.Index]; ok {
			sum += v * e.Value
		}
	}
	return sum
}

func TestDotBillionIndexSteps(t *testing.T) {
	a := Vector{{1, 3}, {500_000_000, -2}, {1_000_000_000, 0.5}}
	b := Vector{{7, 4}, {500_000_000, 1.5}, {1_000_000_000, 2}}

	got, stats, err := Dot(a, b)
	if err != nil {
		t.Fatalf("Dot: %v", err)
	}
	if stats.Steps >= 10 {
		t.Fatalf("steps = %d, want single-digit merge steps", stats.Steps)
	}
	if stats.Steps > len(a)+len(b) {
		t.Fatalf("steps = %d exceeds len(a)+len(b) = %d", stats.Steps, len(a)+len(b))
	}
	want := naiveDot(a, b)
	if got != want {
		t.Fatalf("dot = %v, naive expansion = %v", got, want)
	}
}

func TestDotMatchesNaiveSmallScale(t *testing.T) {
	a := Vector{{0, 1.5}, {3, -2}, {9, 0.25}}
	b := Vector{{1, 8}, {3, 4}, {9, -0.5}}
	got, _, err := Dot(a, b)
	if err != nil {
		t.Fatalf("Dot: %v", err)
	}
	if want := naiveDot(a, b); got != want {
		t.Fatalf("dot = %v, want %v", got, want)
	}
}

func TestDotExplicitZerosCountedAndNeutral(t *testing.T) {
	withZero := Vector{{1, 2}, {5, 0}, {9, 3}}
	withoutZero := Vector{{1, 2}, {9, 3}}
	b := Vector{{1, 7}, {5, 11}, {9, 13}}

	got, stats, err := Dot(withZero, b)
	if err != nil {
		t.Fatalf("Dot: %v", err)
	}
	if stats.ExplicitZeros != 1 {
		t.Fatalf("ExplicitZeros = %d, want 1", stats.ExplicitZeros)
	}
	want, _, err := Dot(withoutZero, b)
	if err != nil {
		t.Fatalf("Dot: %v", err)
	}
	if math.Float64bits(got) != math.Float64bits(want) {
		t.Fatalf("explicit zero changed result: %v vs %v", got, want)
	}
}

func TestDotDoesNotModifyInputs(t *testing.T) {
	a := Vector{{2, 1}, {4, -3}, {6, 5}}
	b := Vector{{4, 2}, {6, 1}}
	aCopy := slices.Clone(a)
	bCopy := slices.Clone(b)

	if _, _, err := Dot(a, b); err != nil {
		t.Fatalf("Dot: %v", err)
	}
	if _, _, err := Cosine(a, b); err != nil {
		t.Fatalf("Cosine: %v", err)
	}
	if !slices.Equal(a, aCopy) || !slices.Equal(b, bCopy) {
		t.Fatalf("inputs modified: a=%v b=%v", a, b)
	}
}

func TestDotRepeatableBitwise(t *testing.T) {
	a := Vector{{0, 1e16}, {3, 1}, {8, -1e16}, {12, 42}}
	b := Vector{{0, 1}, {3, 1}, {8, 1}, {12, 0.5}}
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
			t.Fatalf("run %d: %v != %v", i, got, first)
		}
	}
}
