package coalesce_test

import (
	"math"
	"math/rand/v2"
	"testing"

	"ontology/coalesce"
)

func countComparisons(n int) int64 {
	rng := rand.New(rand.NewPCG(uint64(n), uint64(n*7+1)))
	ivs := randomClippedIntervals(rng, n, n*10)
	nr := coalesce.NewNormalizer()
	if _, err := nr.Normalize(toSpecs(ivs), int64(n*10)); err != nil {
		panic(err)
	}
	return nr.CompareCount()
}

// TestComparisonComplexity：n=100 与 n=10000 两组实测比较次数。
// 要求两组都不超过 n*ceil(log2(n))，且增长不超过 n log n 量级，
// 即实测比值 C(10000)/C(100) 不得超过理论 n log n 比值的一个宽松常数。
func TestComparisonComplexity(t *testing.T) {
	const n1, n2 = 100, 10000
	c1 := countComparisons(n1)
	c2 := countComparisons(n2)

	bound1 := int64(n1) * int64(math.Ceil(math.Log2(n1)))
	bound2 := int64(n2) * int64(math.Ceil(math.Log2(n2)))
	if c1 > bound1 {
		t.Errorf("n=%d comparisons=%d exceed bound %d", n1, c1, bound1)
	}
	if c2 > bound2 {
		t.Errorf("n=%d comparisons=%d exceed bound %d", n2, c2, bound2)
	}

	ratio := float64(c2) / float64(c1)
	theoretical := (float64(n2) * math.Log2(n2)) / (float64(n1) * math.Log2(n1))
	// 实测比值不应显著超过理论 n log n 比值（留 1.25 的常数余量）。
	if ratio > theoretical*1.25 {
		t.Errorf("growth ratio %.2f exceeds n*log n ratio %.2f", ratio, theoretical)
	}
	t.Logf("n=%d comparisons=%d (bound %d); n=%d comparisons=%d (bound %d); ratio %.2f vs nlogn %.2f",
		n1, c1, bound1, n2, c2, bound2, ratio, theoretical)
}
