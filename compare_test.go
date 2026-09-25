package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

// countingComparator counts every value comparison it performs.
type countingComparator struct {
	count int
}

func (c *countingComparator) compare(a, b float64) int {
	c.count++
	return CompareFloat(a, b)
}

func TestComparisonCountBound(t *testing.T) {
	for _, n := range []int{1, 2, 3, 7, 16, 100, 1000} {
		rng := rand.New(rand.NewSource(int64(n)))
		rows := make([]Row, n)
		for i := range rows {
			// Repeated values too: comparisons must stay bounded.
			rows[i] = Row{
				Partition: ptr("p"),
				Value:     float64(rng.Intn(n/2 + 1)),
				ID:        fmt.Sprintf("id%04d", i),
			}
		}

		counter := &countingComparator{}
		res := Rank(rows, Options{Compare: counter.compare})

		bound := ComparisonBound(n)
		if counter.count > bound {
			t.Errorf("n=%d: %d comparisons exceeds bound %d",
				n, counter.count, bound)
		}
		if len(res.Rows) != n {
			t.Errorf("n=%d: ranked %d rows, want %d", n, len(res.Rows), n)
		}
		// Sanity-check the bound helper itself.
		if got := ComparisonBound(n); got != 10*n*wantCeilLog2(n+1) {
			t.Errorf("ComparisonBound(%d)=%d, want %d", n, got, bound)
		}
	}
}

func wantCeilLog2(x int) int {
	k := 0
	p := 1
	for p < x {
		p <<= 1
		k++
	}
	return k
}
