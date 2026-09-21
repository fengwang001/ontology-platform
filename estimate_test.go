package ontology

import (
	"math"
	"testing"
)

// TestEstimateCount 在 n=10000、p=0.01 的数据上，EstimateCount
// 与真实值的相对误差必须在 10% 以内。
func TestEstimateCount(t *testing.T) {
	const n = 10000
	f := fillFilter(t, n, 0.01)
	est := f.EstimateCount()
	rel := math.Abs(est-n) / n
	t.Logf("EstimateCount = %.1f, true = %d, rel err = %.4f", est, n, rel)
	if rel > 0.10 {
		t.Fatalf("relative error %.4f exceeds 10%%", rel)
	}
}

// TestEstimateCountAfterMerge 合并后的过滤器同样能读出估计
// 元素个数，且与真实总数接近。
func TestEstimateCountAfterMerge(t *testing.T) {
	const (
		n     = 10000
		split = 4000
	)
	a, err := New(n, 0.01)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	b, err := New(n, 0.01)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	for i := 0; i < split; i++ {
		a.Add(insElem(i))
	}
	for i := split; i < n; i++ {
		b.Add(insElem(i))
	}
	merged, err := Merge(a, b)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	est := merged.EstimateCount()
	if rel := math.Abs(est-n) / n; rel > 0.10 {
		t.Fatalf("merged EstimateCount = %.1f, rel err %.4f exceeds 10%%", est, rel)
	}
}
