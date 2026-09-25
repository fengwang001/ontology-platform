package sparse

import (
	"errors"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func TestStepsAreSingleDigitForBillionIndices(t *testing.T) {
	a := &Vector{Elems: []Element{
		{Index: 0, Value: 1},
		{Index: 500_000_000, Value: 2},
		{Index: 1_000_000_000, Value: 3},
	}}
	b := &Vector{Elems: []Element{
		{Index: 1, Value: 1},
		{Index: 500_000_000, Value: 4},
		{Index: 1_000_000_000, Value: 5},
	}}
	dot, stats, err := Dot(a, b)
	if err != nil {
		t.Fatalf("Dot: %v", err)
	}
	if stats.Steps >= 10 {
		t.Fatalf("Steps = %d, want single digit (no dense expansion)", stats.Steps)
	}
	if stats.Steps != 4 {
		t.Fatalf("Steps = %d, want exactly 4", stats.Steps)
	}
	if want := 2.0*4 + 3.0*5; dot != want {
		t.Fatalf("dot = %v, want %v", dot, want)
	}
}

func TestDotMatchesNaiveDenseExpansion(t *testing.T) {
	a := &Vector{Elems: []Element{
		{Index: 0, Value: 1.5},
		{Index: 3, Value: -2.25},
		{Index: 7, Value: 0.5},
		{Index: 9, Value: 4},
	}}
	b := &Vector{Elems: []Element{
		{Index: 1, Value: 8},
		{Index: 3, Value: 0.25},
		{Index: 7, Value: -1.5},
		{Index: 9, Value: 2.5},
	}}
	got, _, err := Dot(a, b)
	if err != nil {
		t.Fatalf("Dot: %v", err)
	}
	naive := naiveDenseDot(a, b)
	if got != naive {
		t.Fatalf("dot = %v, naive expansion = %v", got, naive)
	}
}

// naiveDenseDot expands both vectors into dense maps and sums products
// in increasing index order.
func naiveDenseDot(a, b *Vector) float64 {
	da := map[uint32]float64{}
	for _, e := range a.Elems {
		da[e.Index] = e.Value
	}
	var idxs []uint32
	for _, e := range b.Elems {
		if _, ok := da[e.Index]; ok {
			idxs = append(idxs, e.Index)
		}
	}
	sort.Slice(idxs, func(i, j int) bool { return idxs[i] < idxs[j] })
	var sum float64
	for _, i := range idxs {
		sum += da[i] * mustValue(b, i)
	}
	return sum
}

func mustValue(v *Vector, idx uint32) float64 {
	for _, e := range v.Elems {
		if e.Index == idx {
			return e.Value
		}
	}
	return 0
}

func TestDotOrderIndependentBitExact(t *testing.T) {
	base := []Element{
		{Index: 0, Value: 1e16},
		{Index: 2, Value: 1},
		{Index: 5, Value: -1e16},
		{Index: 8, Value: 3.75},
		{Index: 11, Value: 1e-8},
	}
	other := &Vector{Elems: []Element{
		{Index: 0, Value: 1},
		{Index: 2, Value: 1},
		{Index: 5, Value: 1},
		{Index: 8, Value: 2},
		{Index: 11, Value: 4},
	}}
	want, _, err := Dot(&Vector{Elems: base}, other)
	if err != nil {
		t.Fatalf("Dot: %v", err)
	}
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 50; trial++ {
		shuffled := append([]Element(nil), base...)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		sort.Slice(shuffled, func(i, j int) bool {
			return shuffled[i].Index < shuffled[j].Index
		})
		got, _, err := Dot(&Vector{Elems: shuffled}, other)
		if err != nil {
			t.Fatalf("Dot: %v", err)
		}
		if math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("trial %d: bits differ: got %x want %x",
				trial, math.Float64bits(got), math.Float64bits(want))
		}
	}
}

func TestExplicitZerosCountedAndHarmless(t *testing.T) {
	a := &Vector{Elems: []Element{
		{Index: 0, Value: 0},
		{Index: 1, Value: 2},
	}}
	b := &Vector{Elems: []Element{
		{Index: 0, Value: 3},
		{Index: 1, Value: 0},
		{Index: 2, Value: 0},
	}}
	dot, stats, err := Dot(a, b)
	if err != nil {
		t.Fatalf("Dot: %v", err)
	}
	if stats.ExplicitZeros != 3 {
		t.Fatalf("ExplicitZeros = %d, want 3", stats.ExplicitZeros)
	}
	if dot != 0 {
		t.Fatalf("dot = %v, want 0 (explicit zeros must not contribute)", dot)
	}
}

func TestDotInfiniteValuesReturnError(t *testing.T) {
	cases := []struct {
		name string
		a, b *Vector
	}{
		{"inf product", &Vector{Elems: []Element{{Index: 0, Value: math.Inf(1)}}},
			&Vector{Elems: []Element{{Index: 0, Value: 2}}}},
		{"inf times zero is NaN", &Vector{Elems: []Element{{Index: 0, Value: math.Inf(-1)}}},
			&Vector{Elems: []Element{{Index: 0, Value: 0}}}},
		{"inf cancel is NaN", &Vector{Elems: []Element{
			{Index: 0, Value: math.Inf(1)}, {Index: 1, Value: math.Inf(-1)}}},
			&Vector{Elems: []Element{{Index: 0, Value: 1}, {Index: 1, Value: 1}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Dot(tc.a, tc.b)
			var ne *NumericError
			if !errors.As(err, &ne) {
				t.Fatalf("err = %v, want *NumericError", err)
			}
		})
	}
}

func TestInputsAreNotModified(t *testing.T) {
	a := &Vector{Elems: []Element{
		{Index: 2, Value: 1}, {Index: 5, Value: -3}, {Index: 9, Value: 7},
	}}
	b := &Vector{Elems: []Element{
		{Index: 0, Value: 4}, {Index: 5, Value: 2}, {Index: 9, Value: -1},
	}}
	aCopy := &Vector{Elems: append([]Element(nil), a.Elems...)}
	bCopy := &Vector{Elems: append([]Element(nil), b.Elems...)}
	if _, _, err := Dot(a, b); err != nil {
		t.Fatalf("Dot: %v", err)
	}
	if _, _, err := Cosine(a, b); err != nil {
		t.Fatalf("Cosine: %v", err)
	}
	if !reflect.DeepEqual(a, aCopy) || !reflect.DeepEqual(b, bCopy) {
		t.Fatal("inputs were modified")
	}
}
