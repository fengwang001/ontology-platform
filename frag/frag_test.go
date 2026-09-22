package frag

import (
	"errors"
	"reflect"
	"testing"
)

func mustSet(t *testing.T, total int) *Set {
	t.Helper()
	s, err := NewSet(total)
	if err != nil {
		t.Fatalf("NewSet(%d): %v", total, err)
	}
	return s
}

func apply(t *testing.T, s *Set, off int, data []byte) int {
	t.Helper()
	added, err := s.Plan(off, data)
	if err != nil {
		t.Fatalf("Plan(off=%d, %q): %v", off, data, err)
	}
	s.Apply(off, data)
	return added
}

func TestOutOfOrderAssembly(t *testing.T) {
	s := mustSet(t, 10)
	apply(t, s, 5, []byte("fghij"))
	apply(t, s, 0, []byte("abcde"))
	if !s.Complete() {
		t.Fatal("expected complete")
	}
	if got := string(s.Bytes()); got != "abcdefghij" {
		t.Fatalf("assembled %q", got)
	}
}

func TestDuplicateIsIdempotent(t *testing.T) {
	s := mustSet(t, 10)
	if n := apply(t, s, 2, []byte("cde")); n != 3 {
		t.Fatalf("first insert added %d", n)
	}
	if n := apply(t, s, 2, []byte("cde")); n != 0 {
		t.Fatalf("duplicate added %d bytes", n)
	}
	if s.Received() != 3 {
		t.Fatalf("received %d", s.Received())
	}
	want := []Interval{{2, 5}}
	if got := s.Intervals(); !reflect.DeepEqual(got, want) {
		t.Fatalf("intervals %v", got)
	}
}

func TestConflictReportsRangeAndKeepsState(t *testing.T) {
	s := mustSet(t, 10)
	apply(t, s, 0, []byte("abcde"))
	before := s.Intervals()

	_, err := s.Plan(3, []byte("dZZ"))
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConflictError, got %v", err)
	}
	if ce.Start != 4 || ce.End != 5 {
		t.Fatalf("conflict range [%d,%d)", ce.Start, ce.End)
	}
	if got := s.Intervals(); !reflect.DeepEqual(got, before) {
		t.Fatalf("state polluted: %v", got)
	}
	if s.Received() != 5 {
		t.Fatalf("received changed to %d", s.Received())
	}
}

func TestPartialOverlapMerges(t *testing.T) {
	s := mustSet(t, 15)
	apply(t, s, 0, []byte("0123456789"))
	if n := apply(t, s, 5, []byte("56789abcde")); n != 5 {
		t.Fatalf("overlap insert added %d", n)
	}
	want := []Interval{{0, 15}}
	if got := s.Intervals(); !reflect.DeepEqual(got, want) {
		t.Fatalf("intervals %v", got)
	}
	if !s.Complete() {
		t.Fatal("expected complete")
	}
}

func TestAdjacentIntervalsCoalesce(t *testing.T) {
	s := mustSet(t, 10)
	apply(t, s, 0, []byte("abcde"))
	apply(t, s, 5, []byte("fghij"))
	want := []Interval{{0, 10}}
	if got := s.Intervals(); !reflect.DeepEqual(got, want) {
		t.Fatalf("intervals %v, want single merged %v", got, want)
	}
}

func TestValidationErrorsAreDistinct(t *testing.T) {
	if _, err := NewSet(0); !errors.Is(err, ErrZeroTotal) {
		t.Fatalf("zero total: %v", err)
	}
	s := mustSet(t, 5)
	if _, err := s.Plan(0, nil); !errors.Is(err, ErrEmptyFragment) {
		t.Fatalf("empty fragment: %v", err)
	}
	if _, err := s.Plan(3, []byte("xyz")); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("out of range: %v", err)
	}
	if errors.Is(ErrZeroTotal, ErrEmptyFragment) ||
		errors.Is(ErrEmptyFragment, ErrOutOfRange) ||
		errors.Is(ErrZeroTotal, ErrOutOfRange) {
		t.Fatal("validation errors must be mutually distinguishable")
	}
}
