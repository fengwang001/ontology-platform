package ontology

import (
	"errors"
	"testing"
)

func TestRepeatableRead(t *testing.T) {
	s := NewStore()
	mustWrite(t, s, "e", "p", "a", ts(10), ts(20), ts(1))

	before, err := s.AsOf("e", "p", ts(15), ts(1))
	if err != nil || before.Value != "a" {
		t.Fatalf("initial read: %v %v", before, err)
	}

	mustWrite(t, s, "e", "p", "b", ts(10), ts(20), ts(2))
	mustWrite(t, s, "e", "p", "c", ts(12), ts(18), ts(3))

	after, err := s.AsOf("e", "p", ts(15), ts(1))
	if err != nil || after.Value != before.Value ||
		!after.ValidFrom.Equal(before.ValidFrom) || !after.ValidTo.Equal(before.ValidTo) {
		t.Errorf("historical read changed after later writes: %+v vs %+v (%v)", after, before, err)
	}
	now, err := s.AsOf("e", "p", ts(15), ts(3))
	if err != nil || now.Value != "c" {
		t.Errorf("current read: %v %v, want c", now, err)
	}
	mid, err := s.AsOf("e", "p", ts(15), ts(2))
	if err != nil || mid.Value != "b" {
		t.Errorf("read at tx=2: %v %v, want b", mid, err)
	}
}

func TestThreeNotFoundKindsAreDistinct(t *testing.T) {
	s := NewStore()
	mustWrite(t, s, "e", "p", "a", ts(10), ts(20), ts(5))

	_, err := s.AsOf("ghost", "p", ts(15), ts(9))
	if !errors.Is(err, ErrNoFacts) {
		t.Errorf("unknown entity: want ErrNoFacts, got %v", err)
	}
	_, err = s.AsOf("e", "other", ts(15), ts(9))
	if !errors.Is(err, ErrNoFacts) {
		t.Errorf("unknown property: want ErrNoFacts, got %v", err)
	}

	_, err = s.AsOf("e", "p", ts(30), ts(9))
	if !errors.Is(err, ErrValidOutOfRange) || errors.Is(err, ErrNoFacts) || errors.Is(err, ErrNotYetKnown) {
		t.Errorf("validAt outside intervals: want exactly ErrValidOutOfRange, got %v", err)
	}

	_, err = s.AsOf("e", "p", ts(15), ts(3))
	if !errors.Is(err, ErrNotYetKnown) || errors.Is(err, ErrNoFacts) || errors.Is(err, ErrValidOutOfRange) {
		t.Errorf("covered but not yet known: want exactly ErrNotYetKnown, got %v", err)
	}
}

func TestCorrectionsTrajectory(t *testing.T) {
	s := NewStore()
	mustWrite(t, s, "e", "p", "a", ts(10), ts(20), ts(1))
	mustWrite(t, s, "e", "p", "b", ts(10), ts(20), ts(2))
	mustWrite(t, s, "e", "p", "c", ts(12), ts(18), ts(3))

	got, err := s.Corrections("e", "p", ts(15))
	if err != nil {
		t.Fatalf("corrections: %v", err)
	}
	want := []Correction{
		{Value: "a", TxFrom: ts(1), TxTo: ts(2)},
		{Value: "b", TxFrom: ts(2), TxTo: ts(3)},
		{Value: "c", TxFrom: ts(3)},
	}
	if len(got) != len(want) {
		t.Fatalf("want %d corrections, got %d: %+v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("correction %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	for i := 1; i < len(got); i++ {
		if !got[i].TxFrom.After(got[i-1].TxFrom) {
			t.Errorf("trajectory not strictly ordered by tx time at %d", i)
		}
	}

	// A point outside the third write's range keeps the second value; the
	// residual fact re-asserts it from tx=3 on.
	got2, err := s.Corrections("e", "p", ts(11))
	if err != nil || len(got2) != 3 || got2[2].Value != "b" || !got2[2].TxTo.IsZero() {
		t.Errorf("trajectory at validAt=11: %+v, %v", got2, err)
	}

	if _, err := s.Corrections("ghost", "p", ts(1)); !errors.Is(err, ErrNoFacts) {
		t.Errorf("corrections on unknown entity: want ErrNoFacts, got %v", err)
	}
	if _, err := s.Corrections("e", "p", ts(99)); !errors.Is(err, ErrValidOutOfRange) {
		t.Errorf("corrections outside intervals: want ErrValidOutOfRange, got %v", err)
	}
}
