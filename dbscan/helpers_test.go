package dbscan

import "testing"

func mustInsert(t *testing.T, s *Service, id, x, y int64) *Result {
	t.Helper()
	r, err := s.Insert(id, x, y)
	if err != nil {
		t.Fatalf("Insert(%d): %v", id, err)
	}
	return r
}

func wantChanges(t *testing.T, r *Result, want []Change) {
	t.Helper()
	if len(r.Changes) != len(want) {
		t.Fatalf("changes len=%d want %d (%v)", len(r.Changes), len(want), r.Changes)
	}
	for i := range want {
		if r.Changes[i] != want[i] {
			t.Fatalf("changes[%d]=%v want %v (all=%v)", i, r.Changes[i], want[i], r.Changes)
		}
	}
}

func expectReason(t *testing.T, err error, want Reason) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error %s, got nil", want)
	}
	oe, ok := err.(*OpError)
	if !ok || oe.Reason != want {
		t.Fatalf("want %s got %v", want, err)
	}
}

func findEvent(r *Result, typ EventType) *Event {
	for i := range r.Events {
		if r.Events[i].Type == typ {
			return &r.Events[i]
		}
	}
	return nil
}

func reflectInt64(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
