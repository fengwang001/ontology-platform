package rank_test

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	"ontology/rank"
)

// ceilLog2 returns ceil(log2(x)) for x >= 1.
func ceilLog2(x int) int {
	return int(math.Ceil(math.Log2(float64(x))))
}

// Sorting one partition of n rows must cost O(n log n) value
// comparisons, never O(n^2) pairwise comparisons. A counting
// comparator is injected and the total is bounded by
// 10*n*ceil(log2(n+1)).
func TestComparisonCountBound(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for _, n := range []int{1, 2, 7, 50, 200, 1000, 5000} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			ids := make([]string, n)
			values := make([]float64, n)
			for i := range ids {
				ids[i] = fmt.Sprintf("row-%06d", i)
				// Duplicate values on purpose: ties must not
				// push the implementation into quadratic work.
				values[i] = float64(rng.Intn(n/2 + 1))
			}
			rows := makeRows("p", ids, values)

			calls := 0
			counting := func(a, b float64) int {
				calls++
				switch {
				case a < b:
					return -1
				case a > b:
					return 1
				default:
					return 0
				}
			}
			res := rank.Compute(rows, rank.Options{CompareValues: counting})
			if got := len(onlyPartition(t, res).Rows); got != n {
				t.Fatalf("ranked rows = %d, want %d", got, n)
			}

			bound := 10 * n * ceilLog2(n+1)
			if calls > bound {
				t.Fatalf("comparisons = %d, bound = %d (n=%d): looks super-linear",
					calls, bound, n)
			}
			if n > 1 && calls == 0 {
				t.Fatal("counting comparator was never called: injection broken")
			}
		})
	}
}

// A custom comparator must drive both ordering and tie detection.
func TestCustomComparatorSemantics(t *testing.T) {
	// Rank by absolute value: -2 and 2 tie.
	byAbs := func(a, b float64) int {
		aa, bb := math.Abs(a), math.Abs(b)
		switch {
		case aa < bb:
			return -1
		case aa > bb:
			return 1
		default:
			return 0
		}
	}
	rows := makeRows("p",
		[]string{"a", "b", "c"},
		[]float64{-2, 1, 2})
	p := onlyPartition(t, rank.Compute(rows, rank.Options{CompareValues: byAbs}))
	assertTriples(t, p, [][3]int{
		{1, 1, 1}, // 1
		{2, 2, 2}, // -2
		{3, 2, 2}, // 2
	})
	if got := ids(p); !equalStrings(got, []string{"b", "a", "c"}) {
		t.Errorf("id order = %v, want [b a c]", got)
	}
}
