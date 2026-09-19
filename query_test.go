package ontology

import "testing"

func TestAsOfBothAxesAndRepeatableRead(t *testing.T) {
	s := NewStore()
	if err := s.Put("e", "color", "red", ts(0), ts(100), ts(10)); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("e", "color", "blue", ts(30), ts(70), ts(20)); err != nil {
		t.Fatal(err)
	}

	validAt := ts(50)
	// Before either fact is known.
	if got := s.AsOf("e", "color", validAt, ts(9)); got.Status != StatusNotYetKnown {
		t.Fatalf("txAt=9: %v", got.Status)
	}
	// First version known.
	if got := s.AsOf("e", "color", validAt, ts(15)); got.Status != StatusFound || got.Value != "red" {
		t.Fatalf("txAt=15: %+v", got)
	}
	// Correction known.
	if got := s.AsOf("e", "color", validAt, ts(25)); got.Status != StatusFound || got.Value != "blue" {
		t.Fatalf("txAt=25: %+v", got)
	}

	// Repeatable read: more writes cannot change a historical answer.
	if err := s.Put("e", "color", "green", ts(30), ts(70), ts(30)); err != nil {
		t.Fatal(err)
	}
	if got := s.AsOf("e", "color", validAt, ts(15)); got.Value != "red" {
		t.Fatalf("historical read drifted: %+v", got)
	}
	if got := s.AsOf("e", "color", validAt, ts(25)); got.Value != "blue" {
		t.Fatalf("historical read drifted: %+v", got)
	}

	// Valid-time boundaries: From inclusive, To exclusive.
	if got := s.AsOf("e", "color", ts(30), ts(30)); got.Value != "green" {
		t.Fatalf("at From must hit: %+v", got)
	}
	if got := s.AsOf("e", "color", ts(70), ts(30)); got.Value != "red" {
		t.Fatalf("at To must not hit new interval: %+v", got)
	}
}

func TestTrajectoryStableOrder(t *testing.T) {
	s := NewStore()
	mustPut := func(v string, f, to, at int64) {
		t.Helper()
		if err := s.Put("e", "color", v, ts(f), ts(to), ts(at)); err != nil {
			t.Fatal(err)
		}
	}
	mustPut("red", 0, 100, 10)
	mustPut("blue", 30, 70, 20)
	mustPut("green", 40, 60, 30)

	tr := s.Trajectory("e", "color", ts(50))
	want := []string{"red", "blue", "green"}
	if len(tr) != len(want) {
		t.Fatalf("trajectory len = %d: %+v", len(tr), tr)
	}
	for i, v := range want {
		if tr[i].Value != v {
			t.Fatalf("trajectory[%d] = %q, want %q", i, tr[i].Value, v)
		}
		if i > 0 && !tr[i].Tx.From.After(tr[i-1].Tx.From) {
			t.Fatal("trajectory must be strictly ordered by tx time")
		}
	}
}

func TestNotFoundStatuses(t *testing.T) {
	s := NewStore()
	if err := s.Put("e", "color", "red", ts(10), ts(20), ts(10)); err != nil {
		t.Fatal(err)
	}

	if got := s.AsOf("e", "missing", ts(15), ts(99)); got.Status != StatusNoFacts {
		t.Fatalf("never existed: %v", got.Status)
	}
	if got := s.AsOf("ghost", "color", ts(15), ts(99)); got.Status != StatusNoFacts {
		t.Fatalf("unknown entity: %v", got.Status)
	}
	// validAt at To is outside because of the half-open interval.
	if got := s.AsOf("e", "color", ts(20), ts(99)); got.Status != StatusOutsideValidity {
		t.Fatalf("at To: %v", got.Status)
	}
	if got := s.AsOf("e", "color", ts(5), ts(99)); got.Status != StatusOutsideValidity {
		t.Fatalf("before From: %v", got.Status)
	}
	// validAt hits the interval, but txAt precedes knowledge.
	if got := s.AsOf("e", "color", ts(15), ts(9)); got.Status != StatusNotYetKnown {
		t.Fatalf("not yet known: %v", got.Status)
	}
}
