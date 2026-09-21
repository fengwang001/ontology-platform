package ontology

import "testing"

// makeStreams builds k sorted streams with a total of n distinct values:
// stream s holds s, s+k, s+2k, ...
func makeStreams(k, n int) [][]float64 {
	streams := make([][]float64, k)
	for i := 0; i < n; i++ {
		s := i % k
		streams[s] = append(streams[s], float64(i))
	}
	return streams
}

// comparisonBound is the documented upper bound of Stats.Comparisons:
// each of the n pushes sifts up at most ceil(log2(k)) levels and each of
// the n pops sifts down at most 2*ceil(log2(k)) levels.
func comparisonBound(n, k int) int {
	if k < 2 {
		return 0
	}
	levels := 1
	for size := 2; size < k; size *= 2 {
		levels++
	}
	return 3 * n * levels
}

func TestHeapSizeNeverExceedsStreamCount(t *testing.T) {
	const k, n = 4, 1_000_000
	_, stats, err := Union(Multiset, makeStreams(k, n)...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.MaxHeapSize > k {
		t.Fatalf("heap held %d elements, must never exceed %d streams",
			stats.MaxHeapSize, k)
	}
}

func TestComparisonsAreLinearInTotalLength(t *testing.T) {
	const k, n = 4, 1_000_000
	_, stats, err := Union(Multiset, makeStreams(k, n)...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	bound := comparisonBound(n, k) // 3*n*ceil(log2 k) = 6_000_000
	if stats.Comparisons > bound {
		t.Fatalf("comparisons %d exceed linear bound %d", stats.Comparisons, bound)
	}
	// Doubling n at fixed k must at most double the comparisons.
	_, stats2, err := Union(Multiset, makeStreams(k, 2*n)...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats2.Comparisons > 2*bound {
		t.Fatalf("comparisons %d for 2n exceed 2x bound %d", stats2.Comparisons, 2*bound)
	}
	t.Logf("n=%d comparisons=%d bound=%d", n, stats.Comparisons, bound)
}

func TestMergerStatsDuringMerge(t *testing.T) {
	const k = 5
	m, err := newMerger(makeStreams(k, 10_000))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prev := -1.0
	for {
		_, heapSize := m.stats()
		if heapSize > k {
			t.Fatalf("heap holds %d elements, stream count is %d", heapSize, k)
		}
		it, ok := m.pop()
		if !ok {
			break
		}
		if it.value < prev {
			t.Fatalf("merge output not sorted: %v after %v", it.value, prev)
		}
		prev = it.value
	}
	comparisons, heapSize := m.stats()
	if heapSize != 0 {
		t.Fatalf("heap must be empty after the merge, holds %d", heapSize)
	}
	if comparisons > comparisonBound(10_000, k) {
		t.Fatalf("comparisons %d exceed bound %d", comparisons, comparisonBound(10_000, k))
	}
}

func TestUnionMultisetResultLengthOnMillion(t *testing.T) {
	const k, n = 4, 1_000_000
	got, _, err := Union(Multiset, makeStreams(k, n)...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != n {
		t.Fatalf("union of %d distinct values: got length %d", n, len(got))
	}
}
