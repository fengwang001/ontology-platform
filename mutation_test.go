package ontology

import "testing"

func drain(t *testing.T, s *Store, cursor string, limit int) ([]string, int, string) {
	t.Helper()
	var keys []string
	dropped := 0
	for {
		p, err := s.Scan(cursor, limit)
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		keys = append(keys, pageKeys(p)...)
		dropped += p.Dropped
		cursor = p.Next
		if !p.HasMore {
			return keys, dropped, cursor
		}
	}
}

func contains(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

func TestInsertBeforePositionNotVisible(t *testing.T) {
	s, _ := seed(t, 10)
	p1, _ := s.Scan("", 3) // k000..k002
	s.Put("a000", -1)      // sorts before the current position
	s.Put("k005x", 55)     // sorts after the current position

	rest, _, last := drain(t, s, p1.Next, 3)
	all := append(pageKeys(p1), rest...)
	if contains(all, "a000") || contains(all, "k005x") {
		t.Fatalf("inserted elements leaked into snapshot traversal: %v", all)
	}
	if len(all) != 10 {
		t.Fatalf("got %d elements, want 10", len(all))
	}
	st, err := s.TraversalStats(last)
	if err != nil {
		t.Fatalf("TraversalStats: %v", err)
	}
	if !st.MutatedByInsert || st.MutatedByDelete {
		t.Fatalf("flags = insert:%v delete:%v, want insert only",
			st.MutatedByInsert, st.MutatedByDelete)
	}
	if st.SkippedInserted != 2 || st.SkippedDeleted != 0 {
		t.Fatalf("skipped = ins:%d del:%d, want 2/0",
			st.SkippedInserted, st.SkippedDeleted)
	}
}

func TestDeleteUnscannedDroppedNotTruncated(t *testing.T) {
	s, _ := seed(t, 10)
	p1, _ := s.Scan("", 3) // k000..k002
	s.Delete("k007")       // not yet scanned
	s.Delete("k000")       // already scanned: irrelevant to later pages

	rest, dropped, last := drain(t, s, p1.Next, 3)
	all := append(pageKeys(p1), rest...)
	if contains(all, "k007") {
		t.Fatalf("deleted element appeared: %v", all)
	}
	if len(all) != 9 {
		t.Fatalf("got %d elements, want 9", len(all))
	}
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1 (k007 only)", dropped)
	}
	st, err := s.TraversalStats(last)
	if err != nil {
		t.Fatalf("TraversalStats: %v", err)
	}
	if st.MutatedByInsert || !st.MutatedByDelete {
		t.Fatalf("flags = insert:%v delete:%v, want delete only",
			st.MutatedByInsert, st.MutatedByDelete)
	}
	if st.SkippedDeleted != 2 { // k000 (scanned region) + k007
		t.Fatalf("SkippedDeleted = %d, want 2", st.SkippedDeleted)
	}
}

func TestTruncationAndDropAreDistinct(t *testing.T) {
	s, _ := seed(t, 6)
	p1, _ := s.Scan("", 2)
	if !p1.HasMore || p1.Dropped != 0 {
		t.Fatalf("truncated page: hasMore=%v dropped=%d, want true/0",
			p1.HasMore, p1.Dropped)
	}
	s.Delete("k003")
	p2, _ := s.Scan(p1.Next, 2) // walks k002, k003(dropped), k004
	if p2.Dropped != 1 {
		t.Fatalf("page with deletion: dropped=%d, want 1", p2.Dropped)
	}
	if got := pageKeys(p2); !equalKeys(got, []string{"k002", "k004"}) {
		t.Fatalf("page = %v, want [k002 k004]", got)
	}
	// Truncation is observable via HasMore+Next, drop via Dropped:
	// p1 was truncated (no drop), p2 dropped an element (not truncated away).
	if p1.Next == p2.Next {
		t.Fatalf("cursor did not advance")
	}
}

func TestStatsCleanTraversal(t *testing.T) {
	s, _ := seed(t, 3)
	p, _ := s.Scan("", 2)
	st, err := s.TraversalStats(p.Next)
	if err != nil {
		t.Fatalf("TraversalStats: %v", err)
	}
	if st.MutatedByInsert || st.MutatedByDelete ||
		st.SkippedInserted != 0 || st.SkippedDeleted != 0 {
		t.Fatalf("clean traversal stats = %+v, want all zero/false", st)
	}
}
