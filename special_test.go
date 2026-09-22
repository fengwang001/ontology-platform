package ontology

import (
	"errors"
	"math"
	"testing"
)

func TestNaNRejectedAndCounted(t *testing.T) {
	a := New()
	if err := a.Add(math.NaN()); !errors.Is(err, ErrNaN) {
		t.Fatalf("Add(NaN) err = %v, want ErrNaN", err)
	}
	if got := a.Skipped(); got != 1 {
		t.Fatalf("Skipped = %d, want 1", got)
	}
	if got := a.Count(); got != 0 {
		t.Fatalf("Count = %d, want 0 (NaN must not be counted)", got)
	}
	if _, err := a.Mean(); !errors.Is(err, ErrNoSamples) {
		t.Fatalf("Mean after rejected NaN: err = %v, want ErrNoSamples", err)
	}
	_ = a.Add(1)
	_ = a.Add(3)
	if err := a.Add(math.NaN()); !errors.Is(err, ErrNaN) {
		t.Fatalf("Add(NaN) err = %v, want ErrNaN", err)
	}
	if got := a.Skipped(); got != 2 {
		t.Fatalf("Skipped = %d, want 2", got)
	}
	if m := mustMean(t, a); m != 2 {
		t.Fatalf("Mean = %v, want 2 (NaN must not pollute statistics)", m)
	}
	sv, err := a.SampleVariance()
	if err != nil {
		t.Fatalf("SampleVariance: %v", err)
	}
	if sv != 2 {
		t.Fatalf("SampleVariance = %v, want 2", sv)
	}
}

func TestInfinityPoisonsStatistics(t *testing.T) {
	for _, inf := range []float64{math.Inf(1), math.Inf(-1)} {
		a := New()
		_ = a.Add(1)
		_ = a.Add(2)
		if err := a.Add(inf); err != nil {
			t.Fatalf("Add(%v): unexpected error %v", inf, err)
		}
		if got := a.Count(); got != 3 {
			t.Fatalf("Count = %d, want 3", got)
		}
		for name, get := range map[string]func() (float64, error){
			"Mean": a.Mean, "Variance": a.Variance, "SampleVariance": a.SampleVariance,
		} {
			v, err := get()
			if !errors.Is(err, ErrStatsUnavailable) {
				t.Fatalf("Add(%v) then %s: err = %v, want ErrStatsUnavailable", inf, name, err)
			}
			if math.IsNaN(v) {
				t.Fatalf("Add(%v) then %s returned NaN value instead of an error", inf, name)
			}
		}
		if errors.Is(mustErr(a.Mean), ErrNoSamples) {
			t.Fatal("ErrStatsUnavailable must be distinguishable from ErrNoSamples")
		}
	}
}

func mustErr(get func() (float64, error)) error {
	_, err := get()
	return err
}

func TestInfinityMergePropagatesPoison(t *testing.T) {
	clean := New()
	_ = clean.Add(1)
	poisoned := New()
	_ = poisoned.Add(math.Inf(-1))
	merged := Merge(clean, poisoned)
	if _, err := merged.Mean(); !errors.Is(err, ErrStatsUnavailable) {
		t.Fatalf("merged Mean err = %v, want ErrStatsUnavailable", err)
	}
}

func TestSignedZeroSamples(t *testing.T) {
	a := New()
	_ = a.Add(0.0)
	_ = a.Add(math.Copysign(0, -1))
	if got := a.Count(); got != 2 {
		t.Fatalf("Count = %d, want 2 (+0 and -0 are legal samples)", got)
	}
	if m := mustMean(t, a); m != 0 {
		t.Fatalf("Mean = %v, want 0", m)
	}
	pv, err := a.Variance()
	if err != nil {
		t.Fatalf("Variance: %v", err)
	}
	if pv != 0 {
		t.Fatalf("population Variance = %v, want exact 0", pv)
	}
	sv, err := a.SampleVariance()
	if err != nil {
		t.Fatalf("SampleVariance: %v", err)
	}
	if sv != 0 {
		t.Fatalf("SampleVariance = %v, want exact 0", sv)
	}
}
