package ontology

import (
	"errors"
	"testing"
	"time"
)

// ts builds a deterministic, non-zero time from seconds.
func ts(sec int64) time.Time { return time.Unix(sec, 0).UTC() }

func mustWrite(t *testing.T, s *Store, ent, prop string, v any, vf, vt, tx time.Time) {
	t.Helper()
	if err := s.Write(ent, prop, v, vf, vt, tx); err != nil {
		t.Fatalf("write(%s.%s): %v", ent, prop, err)
	}
}

func TestBoundaryLeftClosedRightOpen(t *testing.T) {
	s := NewStore()
	mustWrite(t, s, "e", "p", "v", ts(10), ts(20), ts(1))

	if _, err := s.AsOf("e", "p", ts(10), ts(1)); err != nil {
		t.Errorf("validAt == from must hit: %v", err)
	}
	if _, err := s.AsOf("e", "p", ts(19), ts(1)); err != nil {
		t.Errorf("validAt just before to must hit: %v", err)
	}
	if _, err := s.AsOf("e", "p", ts(20), ts(1)); !errors.Is(err, ErrValidOutOfRange) {
		t.Errorf("validAt == to must miss, got %v", err)
	}
	if _, err := s.AsOf("e", "p", ts(9), ts(1)); !errors.Is(err, ErrValidOutOfRange) {
		t.Errorf("validAt just before from must miss, got %v", err)
	}
}

func TestEmptyAndInvertedAreDistinctErrors(t *testing.T) {
	s := NewStore()

	err := s.Write("e", "p", "v", ts(5), ts(5), ts(1))
	if !errors.Is(err, ErrEmptyInterval) {
		t.Errorf("from == to: want ErrEmptyInterval, got %v", err)
	}
	if errors.Is(err, ErrInvertedInterval) {
		t.Errorf("from == to must not be ErrInvertedInterval")
	}

	err = s.Write("e", "p", "v", ts(6), ts(5), ts(1))
	if !errors.Is(err, ErrInvertedInterval) {
		t.Errorf("from > to: want ErrInvertedInterval, got %v", err)
	}
	if errors.Is(err, ErrEmptyInterval) {
		t.Errorf("from > to must not be ErrEmptyInterval")
	}

	if _, qerr := s.AsOf("e", "p", ts(5), ts(1)); !errors.Is(qerr, ErrNoFacts) {
		t.Errorf("rejected writes must not change state, got %v", qerr)
	}
}

func TestZeroValidFromRejected(t *testing.T) {
	s := NewStore()
	err := s.Write("e", "p", "v", time.Time{}, ts(10), ts(1))
	if !errors.Is(err, ErrValidFromZero) {
		t.Errorf("zero valid-from: want ErrValidFromZero, got %v", err)
	}
	err = s.Write("e", "p", "v", time.Time{}, time.Time{}, ts(1))
	if !errors.Is(err, ErrValidFromZero) {
		t.Errorf("zero valid-from with infinite to: want ErrValidFromZero, got %v", err)
	}
}

func TestInfiniteToAllowed(t *testing.T) {
	s := NewStore()
	mustWrite(t, s, "e", "p", "v", ts(10), time.Time{}, ts(1))

	f, err := s.AsOf("e", "p", ts(1<<40), ts(1))
	if err != nil || f.Value != "v" {
		t.Errorf("far-future validAt must hit infinite interval: %v %v", f, err)
	}
	if _, err := s.AsOf("e", "p", ts(9), ts(1)); !errors.Is(err, ErrValidOutOfRange) {
		t.Errorf("before from must still miss: %v", err)
	}
}

func TestZeroTxRejected(t *testing.T) {
	s := NewStore()
	err := s.Write("e", "p", "v", ts(1), ts(2), time.Time{})
	if !errors.Is(err, ErrTxZero) {
		t.Errorf("zero txAt: want ErrTxZero, got %v", err)
	}
}
