package ontology

import (
	"errors"
	"testing"
)

// TestCheckInvariantDetectsForwardOrphan corrupts the reverse index and
// expects a precise InvariantError.
func TestCheckInvariantDetectsForwardOrphan(t *testing.T) {
	s := newGraphStore(t)
	mustObjects(t, s, "Person", "p1", "p2")
	mustLink(t, s, "knows", "p1", "p2")

	// Corrupt: drop the reverse entry only.
	s.mu.Lock()
	delete(s.st.rev["knows"]["p2"], "p1")
	s.mu.Unlock()

	err := s.CheckInvariant()
	var ie *InvariantError
	if !errors.As(err, &ie) {
		t.Fatalf("expected *InvariantError, got %v", err)
	}
	if ie.LinkType != "knows" || ie.Side != "forward" ||
		ie.Source != "p1" || ie.Target != "p2" {
		t.Fatalf("wrong invariant context: %+v", ie)
	}
}

// TestCheckInvariantDetectsReverseOrphan corrupts the forward index and
// expects the reverse side to be reported.
func TestCheckInvariantDetectsReverseOrphan(t *testing.T) {
	s := newGraphStore(t)
	mustObjects(t, s, "Person", "p1", "p2")
	mustLink(t, s, "knows", "p1", "p2")

	s.mu.Lock()
	delete(s.st.fwd["knows"]["p1"], "p2")
	s.mu.Unlock()

	err := s.CheckInvariant()
	var ie *InvariantError
	if !errors.As(err, &ie) {
		t.Fatalf("expected *InvariantError, got %v", err)
	}
	if ie.LinkType != "knows" || ie.Side != "reverse" ||
		ie.Source != "p1" || ie.Target != "p2" {
		t.Fatalf("wrong invariant context: %+v", ie)
	}
}

func TestCheckInvariantClean(t *testing.T) {
	s := newCascadeStore(t)
	mustObjects(t, s, "A", "a1")
	mustObjects(t, s, "B", "b1")
	mustObjects(t, s, "C", "c1")
	mustLink(t, s, "ab", "a1", "b1")
	mustLink(t, s, "bc", "b1", "c1")
	if err := s.CheckInvariant(); err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
	if err := s.DeleteObject("a1"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	if err := s.CheckInvariant(); err != nil {
		t.Fatalf("CheckInvariant after cascade: %v", err)
	}
}
