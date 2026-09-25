package rank

import (
	"math/rand"
	"testing"
)

// ceilLog2 returns ceil(log2(n)) for n >= 1.
func ceilLog2(n int) int {
	l := 0
	p := 1
	for p < n {
		p <<= 1
		l++
	}
	return l
}

// comparisonBound is the contract: sorting one partition of n rows
// must stay within 10*n*ceil(log2(n+1)) comparisons, keeping the
// implementation in O(n log n) territory and far from O(n^2).
func comparisonBound(n int) int {
	return 10 * n * ceilLog2(n+1)
}

// TestComparisonCountBound feeds single partitions of several sizes
// through Compute with a counting comparator hook and asserts the
// bound holds for each.
func TestComparisonCountBound(t *testing.T) {
	sizes := []int{1, 2, 7, 100, 1000, 4096}
	for _, n := range sizes {
		rng := rand.New(rand.NewSource(int64(n)))
		rows := make([]Row, n)
		for i := range rows {
			// Many duplicates to stress tie handling.
			rows[i] = Row{
				Partition: strPtr("p"),
				Value:     float64(rng.Intn(n/2 + 1)),
				ID:        int64(i),
			}
		}
		compares := 0
		Compute(rows, Options{OnCompare: func() { compares++ }})
		if bound := comparisonBound(n); compares > bound {
			t.Errorf("n=%d: %d comparisons exceed bound %d", n, compares, bound)
		}
	}
}

// TestComparisonBoundIsQuadraticGuard documents the guard's teeth:
// a quadratic algorithm would blow the bound at large n.
func TestComparisonBoundIsQuadraticGuard(t *testing.T) {
	n := 4096
	quadratic := n * (n - 1) / 2
	if quadratic <= comparisonBound(n) {
		t.Fatalf("bound %d does not exclude O(n^2) at n=%d", comparisonBound(n), n)
	}
}
