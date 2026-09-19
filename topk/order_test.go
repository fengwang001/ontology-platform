package topk

import "testing"

func snapshotIDs(s *Selector) []string {
	snap := s.Snapshot()
	ids := make([]string, len(snap))
	for i, e := range snap {
		ids[i] = e.ID
	}
	return ids
}

func equalIDs(a, b []string) bool {
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

func assertOrder(t *testing.T, s *Selector, want []string) {
	t.Helper()
	got := snapshotIDs(s)
	if !equalIDs(got, want) {
		t.Fatalf("snapshot order = %v, want %v", got, want)
	}
}

func TestDescOrdersByScoreDescending(t *testing.T) {
	s, err := New(3, Desc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("low", 1.0)
	s.Push("high", 9.0)
	s.Push("mid", 5.0)
	s.Push("worse", 0.5)
	assertOrder(t, s, []string{"high", "mid", "low"})
}

func TestAscOrdersByScoreAscending(t *testing.T) {
	s, err := New(3, Asc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("low", 1.0)
	s.Push("high", 9.0)
	s.Push("mid", 5.0)
	s.Push("worse", 10.5)
	assertOrder(t, s, []string{"low", "mid", "high"})
}

// Ties break by ascending ID in Desc; the direction must not flip the
// tie-break into descending ID.
func TestDescTiesBreakByAscendingID(t *testing.T) {
	s, err := New(4, Desc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("delta", 7.0)
	s.Push("alpha", 7.0)
	s.Push("charlie", 7.0)
	s.Push("bravo", 7.0)
	assertOrder(t, s, []string{"alpha", "bravo", "charlie", "delta"})
}

// Same tie-break rule in Asc: equal scores sort by ascending ID, not
// descending.
func TestAscTiesBreakByAscendingID(t *testing.T) {
	s, err := New(4, Asc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("delta", 7.0)
	s.Push("alpha", 7.0)
	s.Push("charlie", 7.0)
	s.Push("bravo", 7.0)
	assertOrder(t, s, []string{"alpha", "bravo", "charlie", "delta"})
}

// Mixed scores and ties: the composite order must hold across both
// dimensions in both directions.
func TestCompositeOrderBothDirections(t *testing.T) {
	push := func(s *Selector) {
		s.Push("b", 2.0)
		s.Push("a", 2.0)
		s.Push("c", 1.0)
		s.Push("d", 3.0)
	}
	desc, _ := New(4, Desc)
	push(desc)
	assertOrder(t, desc, []string{"d", "a", "b", "c"})

	asc, _ := New(4, Asc)
	push(asc)
	assertOrder(t, asc, []string{"c", "a", "b", "d"})
}
