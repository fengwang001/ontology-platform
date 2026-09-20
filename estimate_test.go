package ontology

import (
	"fmt"
	"testing"
)

func trueCount(s *Store, attr string, value any) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key, ok := keyOf(value)
	if !ok {
		return 0
	}
	n := 0
	for _, props := range s.entities {
		v, ok := props[attr]
		if !ok {
			continue
		}
		if vk, ok := keyOf(v); ok && vk == key {
			n++
		}
	}
	return n
}

func buildEstimateStore() *Store {
	s := NewStore("color")
	for i := 0; i < 5000; i++ {
		s.Upsert(fmt.Sprintf("e%05d", i), map[string]any{
			"color": fmt.Sprintf("c%d", i%7),
			"score": i % 11,
		})
	}
	return s
}

func TestEstimateIndexedIsExact(t *testing.T) {
	s := buildEstimateStore()
	for _, v := range []string{"c0", "c3", "c6", "absent"} {
		res := s.Estimate("color", v)
		if !res.Exact {
			t.Fatalf("Estimate(color,%q) not marked exact", v)
		}
		if res.ErrorBound != 0 {
			t.Fatalf("Estimate(color,%q) bound = %d, want 0", v, res.ErrorBound)
		}
		if res.RowsExamined != 0 {
			t.Fatalf("Estimate(color,%q) examined %d rows, want 0", v, res.RowsExamined)
		}
		if got := trueCount(s, "color", v); res.Count != got {
			t.Fatalf("Estimate(color,%q) = %d, true = %d", v, res.Count, got)
		}
	}
}

func TestEstimateNonIndexedWithinBound(t *testing.T) {
	s := buildEstimateStore()
	for _, v := range []any{0, 3, 10, 999} {
		res := s.Estimate("score", v)
		if res.Exact {
			t.Fatalf("Estimate(score,%v) marked exact, want estimate", v)
		}
		truth := trueCount(s, "score", v)
		lo, hi := res.Count-res.ErrorBound, res.Count+res.ErrorBound
		if truth < lo || truth > hi {
			t.Fatalf("Estimate(score,%v) = %d ± %d, true %d outside [%d,%d]",
				v, res.Count, res.ErrorBound, truth, lo, hi)
		}
	}
}

func TestEstimateExaminesSmallSample(t *testing.T) {
	s := buildEstimateStore()
	res := s.Estimate("score", 5)
	n := s.TotalRows()
	if res.RowsExamined != DefaultSampleSize {
		t.Fatalf("RowsExamined = %d, want %d", res.RowsExamined, DefaultSampleSize)
	}
	if res.RowsExamined*10 >= n {
		t.Fatalf("RowsExamined = %d not far below total %d", res.RowsExamined, n)
	}
}

func TestEstimateNilValueIsExactlyZero(t *testing.T) {
	s := buildEstimateStore()
	s.Upsert("nil-score", map[string]any{"color": "c0", "score": nil})
	res := s.Estimate("score", nil)
	if !res.Exact || res.Count != 0 {
		t.Fatalf("Estimate nil = %+v, want exact 0", res)
	}
	res = s.Estimate("color", nil)
	if !res.Exact || res.Count != 0 {
		t.Fatalf("Estimate indexed nil = %+v, want exact 0", res)
	}
}

func TestEstimateEmptyStore(t *testing.T) {
	s := NewStore("color")
	res := s.Estimate("anything", "x")
	if res.Count != 0 {
		t.Fatalf("Estimate on empty store = %+v, want 0", res)
	}
}
