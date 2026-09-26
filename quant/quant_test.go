package quant

import (
	"testing"

	"ontology/gk"
)

// TestQueryVisitedBounded proves Query scans only the summary's tuples,
// not the inserted values: the visited-tuple counter of the last Query
// (a non-exported field, read here from inside the package) stays under
// a small m-independent constant while m grows 100x.
func TestQueryVisitedBounded(t *testing.T) {
	visits := make([]int64, 0, 4)
	for _, m := range []int64{100, 1000, 5000, 10000} {
		s := gk.New(0.05)
		for v := int64(0); v < m; v++ { // ascending, distinct
			s.Insert(v)
			if s.N()%10 == 0 {
				s.Compress()
			}
		}
		e := &Engine{}
		e.Query(s, 0.5)
		visits = append(visits, e.visited.Load())
		if e.visited.Load() > 20 {
			t.Errorf("m=%d: Query visited %d tuples, want <= 20", m, e.visited.Load())
		}
	}
	if visits[3] > visits[0]+10 {
		t.Errorf("visited tuples grow with m: %v", visits)
	}
}
