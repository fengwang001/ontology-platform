package ontology

import (
	"fmt"
	"testing"
)

// TestMemoryBoundIsGroupsTimesN streams 1000 groups x 10000 rows with N=5
// and asserts the held-row count never exceeds groups x N while the
// processed count reaches ten million.
func TestMemoryBoundIsGroupsTimesN(t *testing.T) {
	const (
		numGroups = 1000
		rowsPer   = 10000
		n         = 5
	)
	s, err := New(testConfig(n))
	if err != nil {
		t.Fatal(err)
	}
	for g := 0; g < numGroups; g++ {
		gk := fmt.Sprintf("g%04d", g)
		for i := 0; i < rowsPer; i++ {
			s.Add(map[string]any{"g": gk, "s": float64(i), "t": "x"})
			if i%1000 == 0 {
				held, groups := s.Stats()
				if held > groups*n {
					t.Fatalf("held %d exceeds groups*N=%d", held, groups*n)
				}
			}
		}
	}
	held, groups := s.Stats()
	if groups != numGroups {
		t.Fatalf("want %d groups, got %d", numGroups, groups)
	}
	if held > numGroups*n {
		t.Fatalf("held %d exceeds %d", held, numGroups*n)
	}
	if held != numGroups*n {
		t.Fatalf("want exactly %d held rows, got %d", numGroups*n, held)
	}
	if got := s.Processed(); got != int64(numGroups*rowsPer) {
		t.Fatalf("want %d processed rows, got %d", numGroups*rowsPer, got)
	}
	// Spot-check one group kept its true top-5.
	for _, gs := range s.Snapshot() {
		if gs.Group.Value != "g0500" {
			continue
		}
		for i, want := range []float64{9999, 9998, 9997, 9996, 9995} {
			if gs.Rows[i]["s"] != want {
				t.Fatalf("g0500 row %d: want %v, got %v", i, want, gs.Rows[i]["s"])
			}
		}
	}
}
