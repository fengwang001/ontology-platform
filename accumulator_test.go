package ontology

import (
	"math"
	"testing"
)

// relErr 返回 got 相对 want 的相对误差。
func relErr(got, want float64) float64 {
	return math.Abs(got-want) / math.Abs(want)
}

// 朴素两遍公式（平方和减均值平方），用于对照稳定性差异。
func naiveSampleVariance(xs []float64) float64 {
	var sum, sumSq float64
	for _, x := range xs {
		sum += x
		sumSq += x * x
	}
	n := float64(len(xs))
	return (sumSq - sum*sum/n) / (n - 1)
}

func offsetSamples() []float64 {
	return []float64{1e9 + 0, 1e9 + 1, 1e9 + 2, 1e9 + 3, 1e9 + 4}
}

// 1e9 偏移样本：真实样本方差为 2.5，朴素公式在 float64 下失效，
// 本实现必须与真实值的相对误差在 1e-12 以内。
func TestStabilityOffsetSamples(t *testing.T) {
	xs := offsetSamples()
	if nv := naiveSampleVariance(xs); nv == 2.5 {
		t.Fatalf("前置条件失败：朴素公式在此数据上应失效，却得到 %v", nv)
	}

	a := New()
	for _, x := range xs {
		if err := a.Add(x); err != nil {
			t.Fatalf("Add(%v) 返回错误: %v", x, err)
		}
	}
	sv, err := a.SampleVariance()
	if err != nil {
		t.Fatalf("SampleVariance 返回错误: %v", err)
	}
	if got := relErr(sv, 2.5); got > 1e-12 {
		t.Fatalf("样本方差相对误差 %v 超过 1e-12（got %v, want 2.5）", got, sv)
	}

	mean, err := a.Mean()
	if err != nil {
		t.Fatalf("Mean 返回错误: %v", err)
	}
	if got := relErr(mean, 1e9+2); got > 1e-12 {
		t.Fatalf("均值相对误差 %v 超过 1e-12（got %v）", got, mean)
	}

	pv, err := a.Variance()
	if err != nil {
		t.Fatalf("Variance 返回错误: %v", err)
	}
	if got := relErr(pv, 2.0); got > 1e-12 {
		t.Fatalf("总体方差相对误差 %v 超过 1e-12（got %v, want 2.0）", got, pv)
	}
}

// 全部样本相同：方差必须是精确的 0，不是 -1e-9 之类的负数。
func TestIdenticalSamplesVarianceExactlyZero(t *testing.T) {
	for _, v := range []float64{0, 1, -3.5, 1e9, 1e-300, math.MaxFloat64 / 4} {
		a := New()
		for i := 0; i < 1000; i++ {
			if err := a.Add(v); err != nil {
				t.Fatalf("Add(%v) 返回错误: %v", v, err)
			}
		}
		pv, err := a.Variance()
		if err != nil {
			t.Fatalf("Variance 返回错误: %v", err)
		}
		if pv != 0 {
			t.Fatalf("全等样本 %v 的总体方差 = %v，应为精确的 0", v, pv)
		}
		sv, err := a.SampleVariance()
		if err != nil {
			t.Fatalf("SampleVariance 返回错误: %v", err)
		}
		if sv != 0 {
			t.Fatalf("全等样本 %v 的样本方差 = %v，应为精确的 0", v, sv)
		}
	}
}

// 常规数据上均值与方差的基本正确性。
func TestBasicStats(t *testing.T) {
	a := New()
	for i := 1; i <= 10; i++ {
		if err := a.Add(float64(i)); err != nil {
			t.Fatalf("Add 返回错误: %v", err)
		}
	}
	if got := a.Count(); got != 10 {
		t.Fatalf("Count = %d, want 10", got)
	}
	mean, _ := a.Mean()
	if mean != 5.5 {
		t.Fatalf("Mean = %v, want 5.5", mean)
	}
	pv, _ := a.Variance()
	if got := relErr(pv, 8.25); got > 1e-12 {
		t.Fatalf("总体方差 = %v, want 8.25", pv)
	}
	sv, _ := a.SampleVariance()
	if got := relErr(sv, 9.166666666666666); got > 1e-12 {
		t.Fatalf("样本方差 = %v, want 9.166666666666666", sv)
	}
}
