package quantile

import (
	"errors"
	"math"
	"math/rand/v2"
	"testing"
)

type entry struct {
	value  float64
	weight uint64
}

// expandedSketch expands every weighted sample into w unit inserts; it is the
// semantic reference implementation.
func expandedSketch(t *testing.T, entries []entry) *Sketch {
	t.Helper()
	ref := NewSketch()
	for _, e := range entries {
		for i := uint64(0); i < e.weight; i++ {
			if err := ref.Add(e.value); err != nil {
				t.Fatal(err)
			}
		}
	}
	return ref
}

func weightedSketch(t *testing.T, entries []entry) *Sketch {
	t.Helper()
	s := NewSketch()
	for _, e := range entries {
		if err := s.Add(e.value, e.weight); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestWeightMatchesExpansion(t *testing.T) {
	entries := []entry{
		{-3.5, 1}, {1, 3}, {2.25, 7}, {10, 2}, {100, 11},
	}
	s := weightedSketch(t, entries)
	ref := expandedSketch(t, entries)

	if s.TotalWeight() != ref.TotalWeight() {
		t.Fatalf("total weight %d != %d", s.TotalWeight(), ref.TotalWeight())
	}
	if s.UniqueCount() != len(entries) {
		t.Fatalf("unique %d, want %d (no expansion)", s.UniqueCount(), len(entries))
	}

	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 2000; i++ {
		p := rng.Float64()
		a, err := s.QuantileNearestRank(p)
		if err != nil {
			t.Fatal(err)
		}
		b, err := ref.QuantileNearestRank(p)
		if err != nil {
			t.Fatal(err)
		}
		if bits(a) != bits(b) {
			t.Fatalf("p=%.20g nearest: %v != expanded %v", p, a, b)
		}
		c, err := s.QuantileLinear(p)
		if err != nil {
			t.Fatal(err)
		}
		d, err := ref.QuantileLinear(p)
		if err != nil {
			t.Fatal(err)
		}
		if bits(c) != bits(d) {
			t.Fatalf("p=%.20g linear: %v != expanded %v", p, c, d)
		}
	}
}

func TestMillionWeightSmallUniverse(t *testing.T) {
	s := NewSketch()
	values := []entry{{0, 800000}, {1, 250000}, {2, 150000}, {3, 49999}, {4, 1}}
	for _, e := range values {
		if err := s.Add(e.value, e.weight); err != nil {
			t.Fatal(err)
		}
	}
	if s.TotalWeight() != 1_250_000 {
		t.Fatalf("total = %d", s.TotalWeight())
	}
	if s.UniqueCount() != len(values) {
		t.Fatalf("unique = %d, want 5 with >1e6 total weight", s.UniqueCount())
	}
	// Must be bit-identical to a fully expanded reference sketch.
	ref := expandedSketch(t, values)
	for i := 0; i <= 1000; i++ {
		p := float64(i) / 1000
		nr, err := s.QuantileNearestRank(p)
		if err != nil {
			t.Fatal(err)
		}
		wantNR, err := ref.QuantileNearestRank(p)
		if err != nil {
			t.Fatal(err)
		}
		if bits(nr) != bits(wantNR) {
			t.Fatalf("p=%v nr %v != expanded %v", p, nr, wantNR)
		}
		lin, err := s.QuantileLinear(p)
		if err != nil {
			t.Fatal(err)
		}
		wantLin, err := ref.QuantileLinear(p)
		if err != nil {
			t.Fatal(err)
		}
		if bits(lin) != bits(wantLin) {
			t.Fatalf("p=%v lin %v != expanded %v", p, lin, wantLin)
		}
	}
	// Sanity: interpolation near the top yields something strictly between 3
	// and 4 while nearest rank stays on a real sample.
	lin, err := s.QuantileLinear(0.9999996)
	if err != nil || !(lin > 3 && lin < 4) {
		t.Fatalf("tail linear = %v, %v, want a value in (3,4)", lin, err)
	}
}

func TestInvalidWeight(t *testing.T) {
	s := NewSketch()
	if err := s.Add(1, 0); !errors.Is(err, ErrInvalidWeight) {
		t.Fatalf("zero weight: %v", err)
	}
	if err := s.AddWeighted(1, -2); !errors.Is(err, ErrInvalidWeight) {
		t.Fatalf("negative weight: %v", err)
	}
	for _, w := range []float64{1.5, 0.0001, math.NaN(), math.Inf(1), 1e20} {
		if err := s.AddWeighted(1, w); !errors.Is(err, ErrInvalidWeight) {
			t.Fatalf("weight %v: %v", w, err)
		}
	}
	if err := s.Add(1, 2, 3); !errors.Is(err, ErrInvalidWeight) {
		t.Fatalf("extra weights: %v", err)
	}
	if s.TotalWeight() != 0 || s.UniqueCount() != 0 {
		t.Fatal("rejected weights must not be stored")
	}
	// A valid integer-valued float weight is accepted.
	if err := s.AddWeighted(7, 4); err != nil {
		t.Fatal(err)
	}
	if s.TotalWeight() != 4 {
		t.Fatalf("total = %d", s.TotalWeight())
	}
}
