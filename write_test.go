package ontology

import (
	"errors"
	"testing"
	"time"
)

func TestPutRejectsBadIntervals(t *testing.T) {
	s := NewStore()
	if err := s.Put("e", "a", "v", time.Time{}, ts(10), ts(1)); !errors.Is(err, ErrZeroFrom) {
		t.Fatalf("zero from: %v", err)
	}
	if err := s.Put("e", "a", "v", ts(5), ts(5), ts(1)); !errors.Is(err, ErrEmptyInterval) {
		t.Fatalf("empty: %v", err)
	}
	if err := s.Put("e", "a", "v", ts(6), ts(5), ts(1)); !errors.Is(err, ErrReversedInterval) {
		t.Fatalf("reversed: %v", err)
	}
	if got := s.AsOf("e", "a", ts(5), ts(2)); got.Status != StatusNoFacts {
		t.Fatalf("failed writes must not change state, got %v", got.Status)
	}
}

func TestPutMiddleOverlapSplitsIntoTwo(t *testing.T) {
	s := NewStore()
	if err := s.Put("e", "a", "old", ts(0), ts(100), ts(1)); err != nil {
		t.Fatal(err)
	}
	// New valid interval lands strictly inside the old one.
	if err := s.Put("e", "a", "new", ts(30), ts(70), ts(2)); err != nil {
		t.Fatal(err)
	}

	cur := s.CurrentRecords("e", "a")
	want := []Record{
		{Valid: Interval{From: ts(0), To: ts(30)}, Value: "old"},
		{Valid: Interval{From: ts(30), To: ts(70)}, Value: "new"},
		{Valid: Interval{From: ts(70), To: ts(100)}, Value: "old"},
	}
	if len(cur) != len(want) {
		t.Fatalf("got %d current records: %+v", len(cur), cur)
	}
	for i, w := range want {
		if !cur[i].Valid.From.Equal(w.Valid.From) || !cur[i].Valid.To.Equal(w.Valid.To) || cur[i].Value != w.Value {
			t.Fatalf("residual %d = %v %q, want %v %q", i, cur[i].Valid, cur[i].Value, w.Valid, w.Value)
		}
		if !cur[i].Tx.From.Equal(ts(2)) || !cur[i].Tx.To.IsZero() {
			t.Fatalf("residual %d tx = %v, want [2, inf)", i, cur[i].Tx)
		}
	}
}

func TestPutBoundarySplitNoUnitShift(t *testing.T) {
	s := NewStore()
	if err := s.Put("e", "a", "old", ts(10), ts(40), ts(1)); err != nil {
		t.Fatal(err)
	}
	// Right edge exactly coincides: only a left residual must survive.
	if err := s.Put("e", "a", "new", ts(20), ts(40), ts(2)); err != nil {
		t.Fatal(err)
	}
	cur := s.CurrentRecords("e", "a")
	if len(cur) != 2 {
		t.Fatalf("got %d records: %+v", len(cur), cur)
	}
	if !cur[0].Valid.From.Equal(ts(10)) || !cur[0].Valid.To.Equal(ts(20)) {
		t.Fatalf("left residual boundary drifted: %v", cur[0].Valid)
	}
	if !cur[1].Valid.From.Equal(ts(20)) || !cur[1].Valid.To.Equal(ts(40)) {
		t.Fatalf("new interval boundary drifted: %v", cur[1].Valid)
	}
}

func TestPutTxTimeMonotonicPerEntity(t *testing.T) {
	s := NewStore()
	if err := s.Put("e1", "a", "v", ts(0), ts(10), ts(5)); err != nil {
		t.Fatal(err)
	}
	// Different entities have independent clocks.
	if err := s.Put("e2", "a", "v", ts(0), ts(10), ts(1)); err != nil {
		t.Fatalf("other entity at earlier time: %v", err)
	}
	// Same entity: earlier time rejected.
	if err := s.Put("e1", "b", "v", ts(0), ts(10), ts(4)); !errors.Is(err, ErrTxNotAdvancing) {
		t.Fatalf("earlier tx time: %v", err)
	}
	// Same entity: equal time rejected (must be strictly advancing).
	if err := s.Put("e1", "b", "v", ts(0), ts(10), ts(5)); !errors.Is(err, ErrTxNotAdvancing) {
		t.Fatalf("equal tx time: %v", err)
	}
	// State of the rejected attribute untouched.
	if s.AsOf("e1", "b", ts(1), ts(9)).Status != StatusNoFacts {
		t.Fatal("rejected write must not create state")
	}
}
