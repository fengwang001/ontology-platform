package frag

import (
	"errors"
	"testing"
)

// TestConflictSpanCoversAllOverlappingIntervals pins the contract that a
// ConflictError reports the full differing range across every received
// interval the fragment overlaps, not just the first mismatching segment:
// Start is the first differing byte and End is just past the last one.
func TestConflictSpanCoversAllOverlappingIntervals(t *testing.T) {
	s := mustSet(t, 12)
	if _, err := s.Add(0, []byte("AAAA"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(8, []byte("BBBB"), nil); err != nil {
		t.Fatal(err)
	}
	ivsBefore := s.Intervals()
	recvBefore := s.Received()

	// Differs from both received segments: [0,4) and [8,12).
	_, err := s.Add(0, []byte("XXXXccccYYYY"), nil)
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want *ConflictError", err)
	}
	if ce.Start != 0 || ce.End != 12 {
		t.Fatalf("conflict span = [%d,%d), want [0,12)", ce.Start, ce.End)
	}

	// A rejected conflict must not pollute received state.
	if got := s.Intervals(); !equalIntervals(got, ivsBefore) {
		t.Fatalf("intervals changed after conflict: %v -> %v", ivsBefore, got)
	}
	if got := s.Received(); got != recvBefore {
		t.Fatalf("received changed after conflict: %d -> %d", recvBefore, got)
	}
}

func equalIntervals(a, b []Interval) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
