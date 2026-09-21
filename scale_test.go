package ontology

import (
	"math"
	"testing"
)

// 生成一条长度为 n 的升序流：start, start+step, ...
func makeStream(n int, start, step float64) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = start + float64(i)*step
	}
	return s
}

// 比较次数上界：每个元素入堆、出堆各一次，
// 入堆至多 ceil(log2(k)) 次比较，出堆至多 2*ceil(log2(k)) 次，
// 故 Comparisons <= 3 * N * ceil(log2(k))（k >= 2）。
func comparisonBound(n, k int) int64 {
	if k <= 1 {
		return 0
	}
	levels := int(math.Ceil(math.Log2(float64(k))))
	return int64(3 * n * levels)
}

// 总长度一百万、只有 4 条流：堆内元素数峰值不得超过 4，
// 比较次数不得超过线性上界。
func TestScaleMemoryAndComparisons(t *testing.T) {
	const k = 4
	const perStream = 250_000
	const total = k * perStream

	streams := make([][]float64, k)
	for i := range streams {
		streams[i] = makeStream(perStream, float64(i), k)
	}

	got, stats, err := Union(Set, streams...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != total {
		t.Fatalf("union length = %d, want %d", len(got), total)
	}
	if stats.MaxHeapSize > k {
		t.Fatalf("max heap size = %d, want <= %d", stats.MaxHeapSize, k)
	}
	bound := comparisonBound(total, k)
	if stats.Comparisons > bound {
		t.Fatalf("comparisons = %d, want <= %d", stats.Comparisons, bound)
	}
	if stats.Comparisons == 0 {
		t.Fatal("expected a positive number of comparisons")
	}
	t.Logf("N=%d k=%d maxHeap=%d comparisons=%d bound=%d",
		total, k, stats.MaxHeapSize, stats.Comparisons, bound)
}

// 条数增加时上界随之增长，但对固定的 k 始终与 N 成线性。
func TestComparisonBoundScalesLinearlyInN(t *testing.T) {
	const k = 8
	const perStream = 100_000
	streams := make([][]float64, k)
	for i := range streams {
		streams[i] = makeStream(perStream, float64(i), k)
	}
	_, stats, err := Union(Multiset, streams...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	total := k * perStream
	if bound := comparisonBound(total, k); stats.Comparisons > bound {
		t.Fatalf("comparisons = %d, want <= %d", stats.Comparisons, bound)
	}
	if stats.MaxHeapSize > k {
		t.Fatalf("max heap size = %d, want <= %d", stats.MaxHeapSize, k)
	}
}
