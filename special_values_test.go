package ontology

import (
	"errors"
	"math"
	"testing"
)

// NaN 被拒绝、计入跳过计数、不污染统计量。
func TestNaNRejectedAndCounted(t *testing.T) {
	a := New()
	for _, x := range []float64{1, 2, 3} {
		if err := a.Add(x); err != nil {
			t.Fatalf("Add(%v): %v", x, err)
		}
	}
	if err := a.Add(math.NaN()); !errors.Is(err, ErrNaNRejected) {
		t.Fatalf("Add(NaN) err = %v, want ErrNaNRejected", err)
	}
	if err := a.Add(math.NaN()); !errors.Is(err, ErrNaNRejected) {
		t.Fatalf("Add(NaN) err = %v, want ErrNaNRejected", err)
	}
	if got := a.Skipped(); got != 2 {
		t.Fatalf("Skipped() = %d, want 2", got)
	}
	if got := a.Count(); got != 3 {
		t.Fatalf("Count() = %d, want 3 (NaN must not be counted)", got)
	}
	mean, err := a.Mean()
	if err != nil {
		t.Fatalf("Mean: %v", err)
	}
	if mean != 2 {
		t.Fatalf("mean = %v, want 2 (NaN must not pollute statistics)", mean)
	}
	sv, err := a.SampleVariance()
	if err != nil {
		t.Fatalf("SampleVariance: %v", err)
	}
	if sv != 1 {
		t.Fatalf("sample variance = %v, want 1", sv)
	}
}

// 无穷样本参与后，统计量必须报「不可用」错误，而不是悄悄返回 NaN。
func TestInfinityMakesStatsUnusable(t *testing.T) {
	for _, inf := range []float64{math.Inf(1), math.Inf(-1)} {
		a := New()
		for _, x := range []float64{1, 2, 3} {
			if err := a.Add(x); err != nil {
				t.Fatalf("Add(%v): %v", x, err)
			}
		}
		if err := a.Add(inf); err != nil {
			t.Fatalf("Add(Inf) should be accepted, got %v", err)
		}
		if got := a.Count(); got != 4 {
			t.Fatalf("Count() = %d, want 4", got)
		}
		for name, fn := range map[string]func() (float64, error){
			"Mean":           a.Mean,
			"Variance":       a.Variance,
			"SampleVariance": a.SampleVariance,
		} {
			v, err := fn()
			if !errors.Is(err, ErrUnusable) {
				t.Fatalf("%s after Inf: err = %v, want ErrUnusable", name, err)
			}
			if math.IsNaN(v) {
				t.Fatalf("%s after Inf returned NaN value", name)
			}
		}
		if errors.Is(ErrUnusable, ErrNoSamples) || errors.Is(ErrUnusable, ErrZeroDegreesOfFreedom) {
			t.Fatal("ErrUnusable must be distinguishable from other errors")
		}
	}
}

// +0.0 与 -0.0 都是合法样本，按数值 0 处理。
func TestSignedZerosAreValidSamples(t *testing.T) {
	a := New()
	if err := a.Add(0.0); err != nil {
		t.Fatalf("Add(+0.0): %v", err)
	}
	if err := a.Add(math.Copysign(0, -1)); err != nil {
		t.Fatalf("Add(-0.0): %v", err)
	}
	if got := a.Count(); got != 2 {
		t.Fatalf("Count() = %d, want 2", got)
	}
	mean, err := a.Mean()
	if err != nil {
		t.Fatalf("Mean: %v", err)
	}
	if mean != 0 {
		t.Fatalf("mean = %v, want 0", mean)
	}
	sv, err := a.SampleVariance()
	if err != nil {
		t.Fatalf("SampleVariance: %v", err)
	}
	if sv != 0 {
		t.Fatalf("sample variance = %v, want 0", sv)
	}
}
