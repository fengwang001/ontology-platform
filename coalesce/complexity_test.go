package coalesce

import (
	"math/bits"
	"math/rand/v2"
	"testing"

	"ontology/rangespec"
)

func TestComparisonCountIsNLogN(t *testing.T) {
	count100 := countFor(t, 100)
	count10000 := countFor(t, 10000)
	bound100 := 100*ceilLog2(100) + 99
	bound10000 := 10000*ceilLog2(10000) + 9999

	if count100 > bound100 {
		t.Fatalf("n=100 comparisons=%d bound=%d", count100, bound100)
	}
	if count10000 > bound10000 {
		t.Fatalf("n=10000 comparisons=%d bound=%d", count10000, bound10000)
	}

	ratio := float64(count10000) / float64(count100)
	normalized100 := float64(count100) / float64(100*ceilLog2(100))
	normalized10000 := float64(count10000) / float64(10000*ceilLog2(10000))
	if normalized10000 > normalized100*1.25 {
		t.Fatalf("comparison growth %v/%v is above n log n", normalized10000, normalized100)
	}
	t.Logf("n=100 comparisons=%d bound=%d; n=10000 comparisons=%d bound=%d; ratio=%.2f",
		count100, bound100, count10000, bound10000, ratio)
}

func countFor(t *testing.T, n int) int {
	t.Helper()
	order := rand.New(rand.NewPCG(uint64(n), uint64(n*2))).Perm(n)
	items := make([]rangespec.Interval, n)
	for i, position := range order {
		items[i] = rangespec.Interval{Start: int64(position * 2), End: int64(position * 2)}
	}
	ranges, count, err := NormalizeCounted(items, int64(n*2+1))
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != n {
		t.Fatalf("normalized len=%d, want %d", len(ranges), n)
	}
	return count
}

func ceilLog2(n int) int {
	if n <= 1 {
		return 0
	}
	return bits.Len(uint(n - 1))
}
