package ontology

import (
	"errors"
	"math"
	"testing"
)

func relErr(got, want float64) float64 {
	return math.Abs(got-want) / math.Abs(want)
}

func mustMean(t *testing.T, a *Accumulator) float64 {
	t.Helper()
	m, err := a.Mean()
	if err != nil {
		t.Fatalf("Mean: unexpected error: %v", err)
	}
	return m
}

func TestOffsetSamplesAccuracy(t *testing.T) {
	a := New()
	for i := 0; i < 5; i++ {
		if err := a.Add(1e9 + float64(i)); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	if got := a.Count(); got != 5 {
		t.Fatalf("Count = %d, want 5", got)
	}
	if m := mustMean(t, a); m != 1e9+2 {
		t.Fatalf("Mean = %v, want %v", m, 1e9+2)
	}
	pv, err := a.Variance()
	if err != nil {
		t.Fatalf("Variance: %v", err)
	}
	if relErr(pv, 2.0) > 1e-12 {
		t.Fatalf("population Variance = %v, want 2.0 (rel err %g)", pv, relErr(pv, 2.0))
	}
	sv, err := a.SampleVariance()
	if err != nil {
		t.Fatalf("SampleVariance: %v", err)
	}
	if relErr(sv, 2.5) > 1e-12 {
		t.Fatalf("SampleVariance = %v, want 2.5 (rel err %g)", sv, relErr(sv, 2.5))
	}
	// The naive sum-of-squares formula collapses on this data; the
	// accumulator must not.
	var sum, sumSq float64
	for i := 0; i < 5; i++ {
		x := 1e9 + float64(i)
		sum += x
		sumSq += x * x
	}
	naive := (sumSq - sum*sum/5) / 4
	if relErr(naive, 2.5) < 0.5 {
		t.Fatalf("test premise broken: naive formula gave %v, expected it to be far from 2.5", naive)
	}
}

func TestIdenticalSamplesVarianceExactlyZero(t *testing.T) {
	a := New()
	for i := 0; i < 1000; i++ {
		_ = a.Add(3.14)
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

func TestVarianceNeverNegative(t *testing.T) {
	a := New()
	for i := 0; i < 100; i++ {
		_ = a.Add(1e16)
		_ = a.Add(1e16 + 2)
	}
	for _, get := range []func() (float64, error){a.Variance, a.SampleVariance} {
		v, err := get()
		if err != nil {
			t.Fatalf("statistic: %v", err)
		}
		if v < 0 {
			t.Fatalf("variance = %v, must never be negative", v)
		}
	}
}

func TestZeroSamplesErrors(t *testing.T) {
	a := New()
	if got := a.Count(); got != 0 {
		t.Fatalf("Count = %d, want 0", got)
	}
	for name, get := range map[string]func() (float64, error){
		"Mean": a.Mean, "Variance": a.Variance, "SampleVariance": a.SampleVariance,
	} {
		if _, err := get(); !errors.Is(err, ErrNoSamples) {
			t.Fatalf("%s on empty: err = %v, want ErrNoSamples", name, err)
		}
	}
}

func TestSingleSample(t *testing.T) {
	a := New()
	_ = a.Add(42.5)
	if m := mustMean(t, a); m != 42.5 {
		t.Fatalf("Mean = %v, want 42.5", m)
	}
	pv, err := a.Variance()
	if err != nil {
		t.Fatalf("Variance: %v", err)
	}
	if pv != 0 {
		t.Fatalf("population Variance = %v, want 0", pv)
	}
	_, err = a.SampleVariance()
	if !errors.Is(err, ErrTooFewSamples) {
		t.Fatalf("SampleVariance with one sample: err = %v, want ErrTooFewSamples", err)
	}
	if errors.Is(err, ErrNoSamples) {
		t.Fatal("ErrTooFewSamples must be distinguishable from ErrNoSamples")
	}
}
