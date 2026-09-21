package streamset

import "testing"

// makeStream builds an ascending stream of count elements, interleaved
// with the other k-1 streams via stride/offset so the merge does real work.
func makeStream(count, stride, offset int) []float64 {
	s := make([]float64, count)
	for i := range s {
		s[i] = float64(i*stride + offset)
	}
	return s
}

func ceilLog2(k int) int {
	n := 0
	for p := 1; p < k; p *= 2 {
		n++
	}
	return n
}

// comparisonBound is the documented upper bound: every element is pushed
// and popped once; push costs <= floor(log2(k)) comparisons, pop costs
// <= 2*floor(log2(k)), so for k >= 2 the total is <= 3*n*ceil(log2(k)).
func comparisonBound(n, k int64) int64 {
	if k < 2 {
		return 0
	}
	return 3 * n * int64(ceilLog2(int(k)))
}

func TestHeapSizeNeverExceedsStreamCount(t *testing.T) {
	const k = 3
	const perStream = 400_000 // 1.2M elements total
	streams := make([][]float64, k)
	for s := range streams {
		streams[s] = makeStream(perStream, k, s)
	}
	m, err := NewMerger(streams)
	if err != nil {
		t.Fatalf("NewMerger: %v", err)
	}
	total := 0
	for {
		_, counts, ok := m.Next()
		if !ok {
			break
		}
		if hs := m.Stats().HeapSize; hs > k {
			t.Fatalf("heap size %d exceeds stream count %d", hs, k)
		}
		for _, c := range counts {
			total += c
		}
	}
	if total != k*perStream {
		t.Fatalf("merged %d elements, want %d", total, k*perStream)
	}
	if max := m.Stats().MaxHeapSize; max > k {
		t.Fatalf("max heap size %d exceeds stream count %d", max, k)
	}
}

func TestComparisonsLinearInTotalLength(t *testing.T) {
	const k = 4
	run := func(perStream int) int64 {
		streams := make([][]float64, k)
		for s := range streams {
			streams[s] = makeStream(perStream, k, s)
		}
		_, stats, err := Union(Multiset, streams...)
		if err != nil {
			t.Fatalf("Union: %v", err)
		}
		return stats.Comparisons
	}
	n1, n2 := int64(250_000*k), int64(500_000*k)
	c1, c2 := run(250_000), run(500_000)
	if c1 <= 0 {
		t.Fatalf("expected positive comparison count, got %d", c1)
	}
	if bound := comparisonBound(n1, k); c1 > bound {
		t.Fatalf("comparisons %d exceed bound 3*n*ceil(log2(k)) = %d", c1, bound)
	}
	if bound := comparisonBound(n2, k); c2 > bound {
		t.Fatalf("comparisons %d exceed bound 3*n*ceil(log2(k)) = %d", c2, bound)
	}
	// Doubling n must at most roughly double the work: linear, not quadratic.
	if c2 > 3*c1 {
		t.Fatalf("comparisons grew super-linearly: n=%d -> %d, 2n=%d -> %d", n1, c1, n2, c2)
	}
}

func TestSingleStreamNeedsNoComparisons(t *testing.T) {
	_, stats, err := Union(Set, makeStream(1000, 1, 0))
	if err != nil {
		t.Fatalf("Union: %v", err)
	}
	if stats.Comparisons != 0 {
		t.Fatalf("k=1 should need 0 comparisons, got %d", stats.Comparisons)
	}
	if stats.MaxHeapSize > 1 {
		t.Fatalf("k=1 max heap size = %d, want <= 1", stats.MaxHeapSize)
	}
}
