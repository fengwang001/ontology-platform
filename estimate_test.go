package ontology

import (
	"fmt"
	"testing"
)

const estN = 20000

func newEstimateStore(t *testing.T) *Store {
	t.Helper()
	s := New("grp")
	for i := 0; i < estN; i++ {
		id := fmt.Sprintf("e%05d", i)
		attrs := map[string]any{"grp": fmt.Sprintf("g%d", i%10)}
		if i%7 == 0 { // 2858 rows carry color=red on an unindexed attr
			attrs["color"] = "red"
		}
		s.Upsert(id, attrs)
	}
	return s
}

func TestEstimateExactOnIndexedAttr(t *testing.T) {
	s := newEstimateStore(t)
	for _, g := range []string{"g0", "g3", "g9"} {
		res := s.Estimate("grp", g)
		truth := len(s.Query("grp", g))
		if !res.Exact {
			t.Fatalf("Estimate(%s) must be marked exact", g)
		}
		if res.Value != truth {
			t.Fatalf("Estimate(%s)=%d, truth=%d", g, res.Value, truth)
		}
		if res.Bound != 0 {
			t.Fatalf("exact estimate must have bound 0, got %d", res.Bound)
		}
		if res.RowsExamined != 0 {
			t.Fatalf("exact estimate must examine 0 rows, got %d", res.RowsExamined)
		}
	}
}

func TestEstimateSampledWithinBound(t *testing.T) {
	s := newEstimateStore(t)
	res := s.Estimate("color", "red")
	if res.Exact {
		t.Fatal("Estimate on unindexed attr must be marked as estimate")
	}
	truth := 0
	for i := 0; i < estN; i++ {
		if i%7 == 0 {
			truth++
		}
	}
	lo, hi := res.Value-res.Bound, res.Value+res.Bound
	if truth < lo || truth > hi {
		t.Fatalf("truth %d outside [%d, %d] (est=%d bound=%d)",
			truth, lo, hi, res.Value, res.Bound)
	}
	if res.Bound <= 0 {
		t.Fatalf("sampled estimate must carry a positive bound, got %d", res.Bound)
	}
}

func TestEstimateExaminesFarFewerRows(t *testing.T) {
	s := newEstimateStore(t)
	res := s.Estimate("color", "red")
	if res.RowsExamined <= 0 {
		t.Fatal("sampled estimate must examine some rows")
	}
	if res.RowsExamined*10 >= estN {
		t.Fatalf("examined %d rows, not far below table size %d",
			res.RowsExamined, estN)
	}
}

func TestEstimateNilValueMatchesNothing(t *testing.T) {
	s := newEstimateStore(t)
	res := s.Estimate("grp", nil)
	if res.Value != 0 || !res.Exact {
		t.Fatalf("nil equality must estimate exactly 0, got %+v", res)
	}
	res = s.Estimate("color", nil)
	if res.Value != 0 || !res.Exact {
		t.Fatalf("nil equality must estimate exactly 0, got %+v", res)
	}
}

func TestEstimateEmptyStore(t *testing.T) {
	s := New("grp")
	res := s.Estimate("color", "red")
	if res.Value != 0 {
		t.Fatalf("empty store estimate = %d, want 0", res.Value)
	}
}
