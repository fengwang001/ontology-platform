package quant

import (
	"testing"

	"ontology/gk"
)

// Query must locate the answer by walking the summary's tuples, not the n
// inserted values: the number of tuples visited stays bounded by a small
// constant as m grows. The visited counter is read directly here (same
// package); no exported API exposes it.
func TestQueryVisitsBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := gk.New(0.3)
		for i := 0; i < m; i++ {
			s.Insert(int64(i))
			s.Compress()
		}
		q := New(s)
		q.Query(0.5)
		if q.visited > 20 {
			t.Fatalf("m=%d: Query visited %d tuples, want <= 20", m, q.visited)
		}
	}
}
