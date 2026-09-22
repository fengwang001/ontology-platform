package coalesce

import (
	"math"
	"math/rand"
	"testing"

	"ontology/rangespec"
)

func randomSpecs(rng *rand.Rand, n int, size int64) []rangespec.Spec {
	specs := make([]rangespec.Spec, n)
	for i := range specs {
		a := rng.Int63n(size)
		b := a + rng.Int63n(size/10)
		specs[i] = rangespec.Spec{Kind: rangespec.Span, From: a, To: b}
	}
	return specs
}

func countFor(t *testing.T, n int) int64 {
	t.Helper()
	rng := rand.New(rand.NewSource(int64(n) * 7919))
	specs := randomSpecs(rng, n, 1<<30)
	ResetCompareCount()
	if _, err := Normalize(specs, 1<<30); err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	return CompareCount()
}

// TestComparisonCountIsNLogN measures the range-comparison counter for
// n=100 and n=10000 and checks the growth stays within the n*log2(n)
// scale: the measured ratio must not exceed the n*log2(n) ratio times a
// constant-factor margin of 2. An O(n^2) pairwise algorithm would grow
// 10000x here, so this bound cleanly separates the complexity classes.
func TestComparisonCountIsNLogN(t *testing.T) {
	const n1, n2 = 100, 10000
	c1 := countFor(t, n1)
	c2 := countFor(t, n2)
	scale1 := float64(n1) * math.Log2(float64(n1))
	scale2 := float64(n2) * math.Log2(float64(n2))
	bound := 2.0 * scale2 / scale1 // ~400
	ratio := float64(c2) / float64(c1)
	t.Logf("n=%d comparisons=%d; n=%d comparisons=%d; ratio=%.1f (n log n bound %.1f)",
		n1, c1, n2, c2, ratio, bound)
	if ratio > bound {
		t.Fatalf("comparison growth ratio %.1f exceeds n log n bound %.1f", ratio, bound)
	}
	// Absolute sanity: c(n) <= 2*n*ceil(log2 n) + n (sort + merge scan).
	absBound := int64(2*n2)*int64(math.Ceil(math.Log2(n2))) + n2
	if c2 > absBound {
		t.Fatalf("c(%d)=%d exceeds absolute bound %d", n2, c2, absBound)
	}
}
