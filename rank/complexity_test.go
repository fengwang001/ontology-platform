package rank

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

// ceilLog2 returns ceil(log2(n+1)); ceilLog2(0) is 0.
func ceilLog2(n int) int {
	if n <= 0 {
		return 0
	}
	return int(math.Ceil(math.Log2(float64(n) + 1)))
}

// ComparisonBound is the allowed number of comparator calls for one
// partition of n rows: 10*n*ceil(log2(n+1)).
func ComparisonBound(n int) int {
	return 10 * n * ceilLog2(n)
}

// A counting comparator proves the implementation sorts in O(n log n)
// and does not degenerate into O(n^2) pairwise comparisons.
func TestComparisonCountBound(t *testing.T) {
	for _, n := range []int{1, 2, 7, 64, 500, 4096} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(n)))
			rows := make([]Row, n)
			for i := range rows {
				rows[i] = Row{
					Partition: strptr("p"),
					Value:     float64(rng.Intn(n/2 + 1)), // force many ties
					ID:        fmt.Sprintf("r%06d", rng.Int()),
				}
			}

			calls := 0
			cfg := Config{
				Compare: func(a, b Row) int {
					calls++
					return compareValues(a, b)
				},
			}
			Rank(rows, cfg)

			if bound := ComparisonBound(n); calls > bound {
				t.Fatalf("comparisons = %d, bound = %d (10*n*ceil(log2(n+1)))", calls, bound)
			}
		})
	}
}

// The bound must also hold in descending mode.
func TestComparisonCountBoundDescending(t *testing.T) {
	const n = 2048
	rng := rand.New(rand.NewSource(99))
	rows := make([]Row, n)
	for i := range rows {
		rows[i] = Row{
			Partition: strptr("p"),
			Value:     rng.Float64(),
			ID:        fmt.Sprintf("r%06d", i),
		}
	}

	calls := 0
	cfg := Config{
		Descending: true,
		Compare: func(a, b Row) int {
			calls++
			return compareValues(a, b)
		},
	}
	Rank(rows, cfg)

	if bound := ComparisonBound(n); calls > bound {
		t.Fatalf("descending comparisons = %d, bound = %d", calls, bound)
	}
}
