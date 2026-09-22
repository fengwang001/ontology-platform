package ontology

import (
	"errors"
	"math"
	"testing"
)

func relErr(got, want float64) float64 {
	if want == 0 {
		return math.Abs(got)
	}
	return math.Abs(got-want) / math.Abs(want)
}

// 朴素公式（平方和减均值平方）在这批数据上会算出 0 甚至负数，
// 用来反衬在线递推的数值稳定性。
func naiveSampleVariance(xs []float64) float64 {
	var sum, sumSq float64
	for _, x := range xs {
		sum += x
		sumSq += x * x
	}
	n := float64(len(xs))
	return (sumSq - sum*sum/n) / (n - 1)
}

// 1e9 偏移样本：真实样本方差 2.5，要求相对误差 < 1e-12。
func TestOffsetSamplesAccuracy(t *testing.T) {
	xs := []float64{1e9, 1e9 + 1, 1e9 + 2, 1e9 + 3, 1e9 + 4}
	if nv := naiveSampleVariance(xs); nv > 0 {
		t.Logf("naive formula gave %v (expected it to collapse to <= 0)", nv)
	}

	a := New()
	for _, x := range xs {
		if err := a.Add(x); err != nil {
			t.Fatalf("Add(%v): %v", x, err)
		}
	}
	if got := a.Count(); got != 5 {
		t.Fatalf("Count() = %d, want 5", got)
	}
	mean, err := a.Mean()
	if err != nil {
		t.Fatalf("Mean: %v", err)
	}
	if relErr(mean, 1e9+2) > 1e-12 {
		t.Fatalf("mean rel err too large: got %v, want %v", mean, 1e9+2)
	}
	sv, err := a.SampleVariance()
	if err != nil {
		t.Fatalf("SampleVariance: %v", err)
	}
	if relErr(sv, 2.5) > 1e-12 {
		t.Fatalf("sample variance rel err too large: got %v, want 2.5", sv)
	}
	pv, err := a.Variance()
	if err != nil {
		t.Fatalf("Variance: %v", err)
	}
	if relErr(pv, 2.0) > 1e-12 {
		t.Fatalf("population variance rel err too large: got %v, want 2.0", pv)
	}
}

// 全等样本：方差必须是精确的 0，不允许出现负数。
func TestIdenticalSamplesVarianceExactlyZero(t *testing.T) {
	for _, v := range []float64{3.14, -2.5, 1e9, 1e-9, 0} {
		a := New()
		for i := 0; i < 1000; i++ {
			if err := a.Add(v); err != nil {
				t.Fatalf("Add(%v): %v", v, err)
			}
		}
		pv, err := a.Variance()
		if err != nil {
			t.Fatalf("Variance: %v", err)
		}
		if pv != 0 {
			t.Fatalf("population variance of identical samples = %v, want exact 0", pv)
		}
		sv, err := a.SampleVariance()
		if err != nil {
			t.Fatalf("SampleVariance: %v", err)
		}
		if sv != 0 {
			t.Fatalf("sample variance of identical samples = %v, want exact 0", sv)
		}
	}
}

// 零样本：均值与两种方差都必须返回 ErrNoSamples，而不是 NaN 或 0。
func TestEmptyAccumulatorErrors(t *testing.T) {
	a := New()
	if got := a.Count(); got != 0 {
		t.Fatalf("Count() = %d, want 0", got)
	}
	if _, err := a.Mean(); !errors.Is(err, ErrNoSamples) {
		t.Fatalf("Mean err = %v, want ErrNoSamples", err)
	}
	if _, err := a.Variance(); !errors.Is(err, ErrNoSamples) {
		t.Fatalf("Variance err = %v, want ErrNoSamples", err)
	}
	if _, err := a.SampleVariance(); !errors.Is(err, ErrNoSamples) {
		t.Fatalf("SampleVariance err = %v, want ErrNoSamples", err)
	}
}

// 单样本：均值是其本身、总体方差为 0、样本方差报自由度错误，
// 且两类错误必须可区分。
func TestSingleSampleErrors(t *testing.T) {
	a := New()
	if err := a.Add(42.5); err != nil {
		t.Fatalf("Add: %v", err)
	}
	mean, err := a.Mean()
	if err != nil {
		t.Fatalf("Mean: %v", err)
	}
	if mean != 42.5 {
		t.Fatalf("mean = %v, want 42.5", mean)
	}
	pv, err := a.Variance()
	if err != nil {
		t.Fatalf("Variance: %v", err)
	}
	if pv != 0 {
		t.Fatalf("population variance = %v, want 0", pv)
	}
	_, err = a.SampleVariance()
	if !errors.Is(err, ErrZeroDegreesOfFreedom) {
		t.Fatalf("SampleVariance err = %v, want ErrZeroDegreesOfFreedom", err)
	}
	if errors.Is(err, ErrNoSamples) {
		t.Fatal("ErrZeroDegreesOfFreedom must be distinguishable from ErrNoSamples")
	}
}
