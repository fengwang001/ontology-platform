package ontology

import (
	"errors"
	"math"
	"testing"
)

func TestNaNRejectedAndCounted(t *testing.T) {
	a := New()
	a.Add(1)
	a.Add(math.NaN())
	a.Add(2)
	a.Add(math.NaN())
	a.Add(math.NaN())
	if got := a.Count(); got != 2 {
		t.Fatalf("Count = %d, want 2", got)
	}
	if got := a.Skipped(); got != 3 {
		t.Fatalf("Skipped = %d, want 3", got)
	}
	mean, err := a.Mean()
	if err != nil {
		t.Fatalf("Mean: %v", err)
	}
	if mean != 1.5 {
		t.Fatalf("Mean = %v, want 1.5 (NaN must not pollute)", mean)
	}
}

func TestInfMarksStatsUnavailable(t *testing.T) {
	for _, inf := range []float64{math.Inf(1), math.Inf(-1)} {
		a := New()
		a.Add(1)
		a.Add(inf)
		a.Add(2)
		if _, err := a.Mean(); !errors.Is(err, ErrStatsUnavailable) {
			t.Fatalf("Mean err = %v, want ErrStatsUnavailable", err)
		}
		if _, err := a.PopulationVariance(); !errors.Is(err, ErrStatsUnavailable) {
			t.Fatalf("PopulationVariance err = %v, want ErrStatsUnavailable", err)
		}
		if _, err := a.SampleVariance(); !errors.Is(err, ErrStatsUnavailable) {
			t.Fatalf("SampleVariance err = %v, want ErrStatsUnavailable", err)
		}
		// The error must be distinguishable from the empty/degenerate
		// cases, and must never surface as a quiet NaN.
		if errors.Is(err, ErrNoSamples) || errors.Is(err, ErrDegenerateFreedom) {
			t.Fatalf("ErrStatsUnavailable aliases another error: %v", err)
		}
	}
}

func TestInfErrorIsSticky(t *testing.T) {
	a := New()
	a.Add(math.Inf(1))
	for i := 0; i < 100; i++ {
		a.Add(float64(i))
	}
	if _, err := a.Mean(); !errors.Is(err, ErrStatsUnavailable) {
		t.Fatalf("Mean err = %v, want sticky ErrStatsUnavailable", err)
	}
}

func TestInfErrorPropagatesThroughMerge(t *testing.T) {
	a, b := New(), New()
	a.Add(math.Inf(-1))
	b.Add(1)
	b.Add(2)
	if _, err := Merge(a, b).Mean(); !errors.Is(err, ErrStatsUnavailable) {
		t.Fatalf("merged Mean err = %v, want ErrStatsUnavailable", err)
	}
}

func TestSignedZerosAreValidSamples(t *testing.T) {
	a := New()
	a.Add(math.Copysign(0, 1))  // +0.0
	a.Add(math.Copysign(0, -1)) // -0.0
	if got := a.Count(); got != 2 {
		t.Fatalf("Count = %d, want 2", got)
	}
	mean, err := a.Mean()
	if err != nil {
		t.Fatalf("Mean: %v", err)
	}
	if mean != 0 {
		t.Fatalf("Mean = %v, want 0", mean)
	}
	pop, err := a.PopulationVariance()
	if err != nil {
		t.Fatalf("PopulationVariance: %v", err)
	}
	if pop != 0 {
		t.Fatalf("PopulationVariance = %v, want exact 0", pop)
	}
}

func TestNaNDoesNotAffectMerge(t *testing.T) {
	a, b := New(), New()
	a.Add(math.NaN())
	a.Add(1)
	b.Add(2)
	m := Merge(a, b)
	if got := m.Skipped(); got != 1 {
		t.Fatalf("merged Skipped = %d, want 1", got)
	}
	mean, err := m.Mean()
	if err != nil {
		t.Fatalf("merged Mean: %v", err)
	}
	if mean != 1.5 {
		t.Fatalf("merged Mean = %v, want 1.5", mean)
	}
}
