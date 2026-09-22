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

func TestOutOfOrderAssembly(t *testing.T) {
	s := mustSet(t, 10)
	if _, err := s.Add(5, []byte("56789"), nil); err != nil {
		t.Fatal(err)
	}
	if s.Complete() {
		t.Fatal("complete before all bytes arrived")
	}
	if _, err := s.Add(0, []byte("01234"), nil); err != nil {
		t.Fatal(err)
	}
	if !s.Complete() {
		t.Fatal("not complete after covering [0,10)")
	}
	if got := string(s.Bytes()); got != "0123456789" {
		t.Fatalf("bytes = %q", got)
	}
}

func TestDuplicateIsIdempotent(t *testing.T) {
	s := mustSet(t, 10)
	if n, err := s.Add(2, []byte("234"), nil); err != nil || n != 3 {
		t.Fatalf("first add: n=%d err=%v", n, err)
	}
	before := s.Intervals()
	n, err := s.Add(2, []byte("234"), nil)
	if err != nil {
		t.Fatalf("duplicate add: %v", err)
	}
	if n != 0 {
		t.Fatalf("duplicate charged %d new bytes, want 0", n)
	}
	if !reflect.DeepEqual(s.Intervals(), before) {
		t.Fatalf("intervals changed after duplicate: %v", s.Intervals())
	}
	if s.Received() != 3 {
		t.Fatalf("received = %d, want 3", s.Received())
	}
}

func TestConflictReportsSpanAndKeepsState(t *testing.T) {
	s := mustSet(t, 10)
	if _, err := s.Add(0, []byte("01234"), nil); err != nil {
		t.Fatal(err)
	}
	before := s.Intervals()
	// Overlaps [3,5): stored "34", incoming "XX".
	_, err := s.Add(3, []byte("XXab"), nil)
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want *ConflictError", err)
	}
	if ce.Start != 3 || ce.End != 5 {
		t.Fatalf("conflict span = [%d,%d), want [3,5)", ce.Start, ce.End)
	}
	if !reflect.DeepEqual(s.Intervals(), before) {
		t.Fatalf("intervals polluted: got %v want %v", s.Intervals(), before)
	}
	if s.Received() != 5 {
		t.Fatalf("received = %d, want 5", s.Received())
	}
	// Identical re-add of the same range still works (no pollution).
	if _, err := s.Add(0, []byte("01234"), nil); err != nil {
		t.Fatalf("re-add after conflict: %v", err)
	}
}

func TestOverlapAndAdjacencyMerge(t *testing.T) {
	s := mustSet(t, 15)
	if _, err := s.Add(0, []byte("0123456789"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(5, []byte("56789abcde"), nil); err != nil {
		t.Fatal(err)
	}
	want := []Interval{{Start: 0, End: 15}}
	if got := s.Intervals(); !reflect.DeepEqual(got, want) {
		t.Fatalf("intervals = %v, want %v", got, want)
	}

	a := mustSet(t, 10)
	if _, err := a.Add(0, []byte("01234"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Add(5, []byte("56789"), nil); err != nil {
		t.Fatal(err)
	}
	want = []Interval{{Start: 0, End: 10}}
	if got := a.Intervals(); !reflect.DeepEqual(got, want) {
		t.Fatalf("adjacent intervals = %v, want merged %v", got, want)
	}
}

func TestValidationErrorsAreDistinct(t *testing.T) {
	if _, err := NewSet(0); !errors.Is(err, ErrZeroTotal) {
		t.Fatalf("NewSet(0) err = %v, want ErrZeroTotal", err)
	}
	s := mustSet(t, 5)
	if _, err := s.Add(0, nil, nil); !errors.Is(err, ErrEmptyData) {
		t.Fatalf("empty err = %v, want ErrEmptyData", err)
	}
	if _, err := s.Add(3, []byte("abc"), nil); !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("overflow err = %v, want ErrOutOfBounds", err)
	}
	if _, err := s.Add(-1, []byte("a"), nil); !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("negative err = %v, want ErrOutOfBounds", err)
	}
	if errors.Is(ErrEmptyData, ErrOutOfBounds) || errors.Is(ErrZeroTotal, ErrEmptyData) {
		t.Fatal("sentinel errors must be mutually distinct")
	}
}

func TestReserveFailureLeavesSetUntouched(t *testing.T) {
	s := mustSet(t, 10)
	if _, err := s.Add(0, []byte("01234"), nil); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("no room")
	_, err := s.Add(5, []byte("56789"), func(int) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	want := []Interval{{Start: 0, End: 5}}
	if got := s.Intervals(); !reflect.DeepEqual(got, want) {
		t.Fatalf("intervals = %v, want %v", got, want)
	}
}
