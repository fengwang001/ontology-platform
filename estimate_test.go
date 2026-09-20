package ontology

import (
	"fmt"
	"testing"
)

const estRows = 20000

// fillCities loads estRows entities with an indexed "color" and an
// unindexed "city" whose distribution is skewed toward "beijing".
func fillCities(s *Store) {
	for i := 0; i < estRows; i++ {
		city := "other"
		switch {
		case i%10 < 5:
			city = "beijing"
		case i%10 < 8:
			city = "shanghai"
		}
		s.Upsert(fmt.Sprintf("r%06d", i), map[string]any{
			"color": fmt.Sprintf("c%d", i%10),
			"city":  city,
		})
	}
}

func TestEstimateExactOnIndexedAttr(t *testing.T) {
	s := NewStore("color")
	fillCities(s)

	est := s.Estimate("color", "c3")
	if !est.Exact {
		t.Fatal("indexed attr must be exact")
	}
	if est.AbsErrorBound != 0 {
		t.Fatalf("exact estimate must have zero bound, got %d", est.AbsErrorBound)
	}
	if est.RowsChecked != 0 {
		t.Fatalf("exact estimate must not scan rows, checked %d", est.RowsChecked)
	}
	if want := len(scanIDs(s, "color", "c3")); est.Rows != want {
		t.Fatalf("estimate %d != true %d", est.Rows, want)
	}
}

func TestEstimateSampledWithinBound(t *testing.T) {
	s := NewStore("color")
	fillCities(s)

	for _, city := range []string{"beijing", "shanghai", "other", "nowhere"} {
		est := s.Estimate("city", city)
		if est.Exact {
			t.Fatal("unindexed attr must be an estimate")
		}
		truth := len(scanIDs(s, "city", city))
		lo, hi := est.Rows-est.AbsErrorBound, est.Rows+est.AbsErrorBound
		if truth < lo || truth > hi {
			t.Fatalf("city %q: truth %d outside [%d,%d]", city, truth, lo, hi)
		}
		if est.RowsChecked > sampleTarget {
			t.Fatalf("checked %d rows > sampleTarget", est.RowsChecked)
		}
		if est.RowsChecked*10 > estRows {
			t.Fatalf("checked %d rows, not far below %d", est.RowsChecked, estRows)
		}
	}
}

func TestEstimateNilValueIsExactZero(t *testing.T) {
	s := NewStore("color")
	fillCities(s)
	est := s.Estimate("city", nil)
	if !est.Exact || est.Rows != 0 {
		t.Fatalf("nil equality must be exact 0, got %+v", est)
	}
}

func TestEstimateEmptyStore(t *testing.T) {
	s := NewStore("color")
	est := s.Estimate("city", "x")
	if est.Rows != 0 {
		t.Fatalf("empty store estimate = %+v", est)
	}
}
