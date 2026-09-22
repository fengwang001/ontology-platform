package frag

import (
	"errors"
	"testing"
)

// TestConflictSpanningMultipleIntervals pins the contract that a
// ConflictError covers the full difference range: when one fragment overlaps
// several received intervals and mismatches in more than one of them, Start
// is the first differing byte and End is just past the last differing byte
// across ALL overlapped intervals, not just the first mismatching one.
func TestConflictSpanningMultipleIntervals(t *testing.T) {
	s := mustSet(t, 12)
	if _, err := s.Add(0, []byte("AAAA"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(8, []byte("BBBB"), nil); err != nil {
		t.Fatal(err)
	}
	_, err := s.Add(0, []byte("XXXXccccYYYY"), nil)
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *ConflictError, got %v", err)
	}
	if ce.Start != 0 || ce.End != 12 {
		t.Fatalf("conflict range = [%d,%d), want [0,12)", ce.Start, ce.End)
	}
	if got := s.Received(); got != 8 {
		t.Fatalf("received after conflict = %d, want 8 (set must be untouched)", got)
	}
	ivs := s.Intervals()
	if len(ivs) != 2 || ivs[0] != (Interval{0, 4}) || ivs[1] != (Interval{8, 12}) {
		t.Fatalf("intervals after conflict = %v, want [{0 4} {8 12}]", ivs)
	}
}
