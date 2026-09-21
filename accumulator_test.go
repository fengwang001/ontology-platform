package ontology

import (
	"errors"
	"math"
	"testing"
)

func relErr(got, want float64) float64 {
	return math.Abs(got-want) / math.Abs(want)
}

// 1e9 + {0,1,2,3,4}：真实样本方差为 2.5。朴素「平方和减均值平方」
// 公式在 float64 下会算出 0 甚至负数，本实现必须在 1e-12 相对误差内。
func TestShiftedSamplesSampleVariance(t *testing.T) {
	var a Accumulator
	var sum, sumSq float64
	for i := 0; i < 5; i++ {
		x := 1e9 + float64(i)
		if err := a.Add(x); err != nil {
			t.Fatalf("Add: %v", err)
		}
		sum += x
		sumSq += x * x
	}

	got, err := a.SampleVariance()
	if err != nil {
		t.Fatalf("SampleVariance: %v", err)
	}
	if e := relErr(got, 2.5); e > 1e-12 {
		t.Fatalf("sample variance = %v, rel err %v > 1e-12", got, e)
	}

	// 确认这批数据确实能让朴素公式垮掉（算出 0 或负数）。
	n := 5.0
	naive := (sumSq - sum*sum/n) / (n - 1)
	if naive > 0 && relErr(naive, 2.5) <= 1e-12 {
		t.Fatalf("naive formula unexpectedly accurate: %v", naive)
	}

	mean, err := a.Mean()
	if err != nil {
		t.Fatalf("Mean: %v", err)
	}
	if e := relErr(mean, 1e9+2); e > 1e-12 {
		t.Fatalf("mean = %v, rel err %v > 1e-12", mean, e)
	}
}

// 全等样本：总体方差与样本方差都必须是精确的 0，不允许出现负数。
func TestIdenticalSamplesVarianceExactlyZero(t *testing.T) {
	var a Accumulator
	for i := 0; i < 1000; i++ {
		if err := a.Add(3.14159); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	pv, err := a.PopulationVariance()
	if err != nil {
		t.Fatalf("PopulationVariance: %v", err)
	}
	sv, err := a.SampleVariance()
	if err != nil {
		t.Fatalf("SampleVariance: %v", err)
	}
	if pv != 0 || sv != 0 {
		t.Fatalf("variances = %v, %v; want exact 0", pv, sv)
	}
	if math.Signbit(pv) || math.Signbit(sv) {
		t.Fatal("variance must not be negative zero")
	}
}

// 零样本：均值与两种方差都返回 ErrNoSamples，且与单样本错误可区分。
func TestZeroSamplesErrors(t *testing.T) {
	var a Accumulator
	if a.Count() != 0 {
		t.Fatalf("Count = %d, want 0", a.Count())
	}
	for name, fn := range map[string]func() (float64, error){
		"Mean":               a.Mean,
		"PopulationVariance": a.PopulationVariance,
		"SampleVariance":     a.SampleVariance,
	} {
		if _, err := fn(); !errors.Is(err, ErrNoSamples) {
			t.Fatalf("%s err = %v, want ErrNoSamples", name, err)
		} else if errors.Is(err, ErrInsufficientSamples) {
			t.Fatalf("%s: ErrNoSamples must not match ErrInsufficientSamples", name)
		}
	}
}

// 恰好一个样本：均值是其本身、总体方差是 0、样本方差报自由度不足。
func TestSingleSample(t *testing.T) {
	var a Accumulator
	if err := a.Add(42.5); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if a.Count() != 1 {
		t.Fatalf("Count = %d, want 1", a.Count())
	}
	mean, err := a.Mean()
	if err != nil || mean != 42.5 {
		t.Fatalf("Mean = %v, %v; want 42.5, nil", mean, err)
	}
	pv, err := a.PopulationVariance()
	if err != nil || pv != 0 {
		t.Fatalf("PopulationVariance = %v, %v; want 0, nil", pv, err)
	}
	if _, err := a.SampleVariance(); !errors.Is(err, ErrInsufficientSamples) {
		t.Fatalf("SampleVariance err = %v, want ErrInsufficientSamples", err)
	} else if errors.Is(err, ErrNoSamples) {
		t.Fatal("ErrInsufficientSamples must not match ErrNoSamples")
	}
}

// NaN 被拒绝：计入跳过计数，不污染统计量。
func TestNaNRejectedAndSkipped(t *testing.T) {
	var a Accumulator
	for _, x := range []float64{1, 2, 3} {
		if err := a.Add(x); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	if err := a.Add(math.NaN()); !errors.Is(err, ErrNaNSample) {
		t.Fatalf("Add(NaN) err = %v, want ErrNaNSample", err)
	}
	if a.Skipped() != 1 {
		t.Fatalf("Skipped = %d, want 1", a.Skipped())
	}
	if a.Count() != 3 {
		t.Fatalf("Count = %d, want 3", a.Count())
	}
	mean, err := a.Mean()
	if err != nil || mean != 2 {
		t.Fatalf("Mean = %v, %v; want 2, nil", mean, err)
	}
	sv, err := a.SampleVariance()
	if err != nil || sv != 1 {
		t.Fatalf("SampleVariance = %v, %v; want 1, nil", sv, err)
	}
}

// ±Inf 参与统计后，读取必须返回明确标注「统计量已不可用」的错误。
func TestInfPoisonsStatistics(t *testing.T) {
	for _, inf := range []float64{math.Inf(1), math.Inf(-1)} {
		var a Accumulator
		_ = a.Add(1)
		if err := a.Add(inf); err != nil {
			t.Fatalf("Add(Inf) err = %v, want nil", err)
		}
		if a.Count() != 2 {
			t.Fatalf("Count = %d, want 2", a.Count())
		}
		for name, fn := range map[string]func() (float64, error){
			"Mean":               a.Mean,
			"PopulationVariance": a.PopulationVariance,
			"SampleVariance":     a.SampleVariance,
		} {
			v, err := fn()
			if !errors.Is(err, ErrStatsUnavailable) {
				t.Fatalf("Add(%v): %s = %v, %v; want ErrStatsUnavailable", inf, name, v, err)
			}
			if math.IsNaN(v) {
				t.Fatalf("Add(%v): %s must not return NaN silently", inf, name)
			}
		}
	}
}

// +0.0 与 -0.0 都是合法样本，按数值 0 处理。
func TestSignedZeroSamples(t *testing.T) {
	var a Accumulator
	if err := a.Add(math.Copysign(0, -1)); err != nil {
		t.Fatalf("Add(-0): %v", err)
	}
	if err := a.Add(0); err != nil {
		t.Fatalf("Add(+0): %v", err)
	}
	if a.Count() != 2 {
		t.Fatalf("Count = %d, want 2", a.Count())
	}
	mean, err := a.Mean()
	if err != nil || mean != 0 {
		t.Fatalf("Mean = %v, %v; want 0, nil", mean, err)
	}
	sv, err := a.SampleVariance()
	if err != nil || sv != 0 {
		t.Fatalf("SampleVariance = %v, %v; want 0, nil", sv, err)
	}
}
