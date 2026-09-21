package ontology

import (
	"errors"
	"math"
	"testing"
)

// 零个样本：均值与两种方差都必须返回 ErrNoSamples，而不是 NaN 或 0。
func TestZeroSamplesErrors(t *testing.T) {
	a := New()
	if got := a.Count(); got != 0 {
		t.Fatalf("Count = %d, want 0", got)
	}
	if _, err := a.Mean(); !errors.Is(err, ErrNoSamples) {
		t.Fatalf("Mean 错误 = %v, want ErrNoSamples", err)
	}
	if _, err := a.Variance(); !errors.Is(err, ErrNoSamples) {
		t.Fatalf("Variance 错误 = %v, want ErrNoSamples", err)
	}
	if _, err := a.SampleVariance(); !errors.Is(err, ErrNoSamples) {
		t.Fatalf("SampleVariance 错误 = %v, want ErrNoSamples", err)
	}
}

// 恰好一个样本：均值是它本身、总体方差是 0，
// 样本方差返回与 ErrNoSamples 可区分的 ErrNoDegreesOfFreedom。
func TestOneSampleErrors(t *testing.T) {
	a := New()
	if err := a.Add(3.25); err != nil {
		t.Fatalf("Add 返回错误: %v", err)
	}
	mean, err := a.Mean()
	if err != nil || mean != 3.25 {
		t.Fatalf("Mean = %v, %v; want 3.25, nil", mean, err)
	}
	pv, err := a.Variance()
	if err != nil || pv != 0 {
		t.Fatalf("Variance = %v, %v; want 0, nil", pv, err)
	}
	_, svErr := a.SampleVariance()
	if !errors.Is(svErr, ErrNoDegreesOfFreedom) {
		t.Fatalf("SampleVariance 错误 = %v, want ErrNoDegreesOfFreedom", svErr)
	}
	if errors.Is(svErr, ErrNoSamples) {
		t.Fatal("单样本样本方差错误不得与零样本错误混淆")
	}
	if errors.Is(ErrNoDegreesOfFreedom, ErrNoSamples) ||
		errors.Is(ErrNoSamples, ErrNoDegreesOfFreedom) {
		t.Fatal("ErrNoSamples 与 ErrNoDegreesOfFreedom 必须可区分")
	}
}

// NaN 样本被拒绝并计入跳过计数，不污染统计量。
func TestNaNRejectedAndCounted(t *testing.T) {
	a := New()
	for _, x := range []float64{1, 2, 3} {
		if err := a.Add(x); err != nil {
			t.Fatalf("Add 返回错误: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		if err := a.Add(math.NaN()); !errors.Is(err, ErrNaN) {
			t.Fatalf("Add(NaN) 错误 = %v, want ErrNaN", err)
		}
	}
	if got := a.Skipped(); got != 3 {
		t.Fatalf("Skipped = %d, want 3", got)
	}
	if got := a.Count(); got != 3 {
		t.Fatalf("Count = %d, want 3（NaN 不得计入）", got)
	}
	mean, err := a.Mean()
	if err != nil || mean != 2 {
		t.Fatalf("Mean = %v, %v; want 2, nil（NaN 不得污染统计量）", mean, err)
	}
	sv, err := a.SampleVariance()
	if err != nil || sv != 1 {
		t.Fatalf("SampleVariance = %v, %v; want 1, nil", sv, err)
	}
}

// 正负无穷参与统计后，读取统计量必须返回 ErrUnavailable，
// 而不是把 NaN 或无穷悄悄返回给调用方。
func TestInfMakesStatsUnavailable(t *testing.T) {
	for _, inf := range []float64{math.Inf(1), math.Inf(-1)} {
		a := New()
		_ = a.Add(1)
		_ = a.Add(inf)
		_ = a.Add(3)
		if got := a.Count(); got != 3 {
			t.Fatalf("Count = %d, want 3（Inf 计入样本数）", got)
		}
		if _, err := a.Mean(); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("Mean 错误 = %v, want ErrUnavailable", err)
		}
		if _, err := a.Variance(); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("Variance 错误 = %v, want ErrUnavailable", err)
		}
		if _, err := a.SampleVariance(); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("SampleVariance 错误 = %v, want ErrUnavailable", err)
		}
		if errors.Is(ErrUnavailable, ErrNoSamples) ||
			errors.Is(ErrUnavailable, ErrNoDegreesOfFreedom) {
			t.Fatal("ErrUnavailable 必须与其他错误可区分")
		}
	}
}

// 不可用状态可通过合并传播：与不可用累加器合并的结果也不可用。
func TestUnavailablePropagatesThroughMerge(t *testing.T) {
	a := New()
	_ = a.Add(math.Inf(1))
	b := fill(t, []float64{1, 2, 3})
	if _, err := Merge(a, b).Mean(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("合并不可用累加器后 Mean 错误 = %v, want ErrUnavailable", err)
	}
}

// +0.0 与 -0.0 都是合法样本，按数值 0 处理。
func TestSignedZerosAreValidSamples(t *testing.T) {
	a := New()
	for _, z := range []float64{0.0, math.Copysign(0, -1), 0.0, math.Copysign(0, -1)} {
		if err := a.Add(z); err != nil {
			t.Fatalf("Add(%v) 返回错误: %v", z, err)
		}
	}
	if got := a.Count(); got != 4 {
		t.Fatalf("Count = %d, want 4", got)
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
