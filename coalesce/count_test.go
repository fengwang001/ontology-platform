package coalesce

import (
	"math"
	"math/rand"
	"testing"

	"ontology/rangespec"
)

// countFor normalizes n random specs and returns the comparison count.
func countFor(n int, rng *rand.Rand) int64 {
	specs := make([]rangespec.Spec, n)
	for i := range specs {
		a := int64(rng.Intn(n))
		specs[i] = rangespec.Spec{First: a, Last: a + int64(rng.Intn(n)), Suffix: -1}
	}
	ResetCompareCount()
	if _, err := Normalize(specs, int64(2*n)); err != nil {
		panic(err)
	}
	return CompareCount()
}

// TestComparisonCountGrowth is the proof for requirement 4: normalization is
// sort-based (O(n log n)), never pairwise (O(n^2)). The measured counts for
// n=100 and n=10000 must grow no faster than the n log n ratio (with slack
// for constant factors), while an O(n^2) algorithm would grow ~10000x.
func TestComparisonCountGrowth(t *testing.T) {
	const n1, n2 = 100, 10000
	c1 := countFor(n1, rand.New(rand.NewSource(1)))
	c2 := countFor(n2, rand.New(rand.NewSource(2)))
	t.Logf("n=%d comparisons=%d; n=%d comparisons=%d", n1, c1, n2, c2)

	nlognRatio := (float64(n2) * math.Log2(float64(n2))) /
		(float64(n1) * math.Log2(float64(n1)))
	measured := float64(c2) / float64(c1)
	t.Logf("n*log(n) ratio=%.1f, measured ratio=%.1f", nlognRatio, measured)

	// Allow 2x slack over the n log n ratio for constant factors. A
	// pairwise O(n^2) merge would show a ratio near (n2/n1)^2 = 10000.
	if measured > 2*nlognRatio {
		t.Fatalf("comparison count grows faster than O(n log n): "+
			"ratio %.1f > 2*%.1f (c1=%d, c2=%d)", measured, nlognRatio, c1, c2)
	}
	// Sanity: an O(n^2) pairwise algorithm on n2 would need ~n2^2/2
	// comparisons; assert we are far below that.
	if c2 > int64(n2)*int64(n2)/10 {
		t.Fatalf("c2=%d looks quadratic for n=%d", c2, n2)
	}
}
