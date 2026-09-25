package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

// TestComparisonBound feeds n rows through a counting comparator and
// requires the comparison count to stay within the O(n log n) bound,
// guarding against an O(n^2) pairwise implementation.
func TestComparisonBound(t *testing.T) {
	for _, n := range []int{1, 7, 100, 1000, 5000} {
		rng := rand.New(rand.NewSource(int64(n)))
		rows := make([]Row, n)
		for i := range rows {
			rows[i] = Row{
				Partition: StringPtr("p"),
				Value:     float64(rng.Intn(50)), // force many ties
				ID:        fmt.Sprintf("id-%06d", i),
			}
		}

		count := 0
		base := DefaultCompare(false)
		sum := Compute(rows, Options{
			Compare: func(a, b Row) int {
				count++
				return base(a, b)
			},
		})

		if len(sum.Rows) != n {
			t.Fatalf("n=%d: got %d ranked rows", n, len(sum.Rows))
		}
		if bound := ComparisonBound(n); count > bound {
			t.Errorf("n=%d: %d comparisons exceeds bound %d", n, count, bound)
		}
	}
}
