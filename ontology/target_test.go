package ontology

import "testing"

// TestMultiViewAtomicVisibility: one commit touches AmountByRegion,
// AmountByCategory and CountByRegion. After the commit's effective point all
// three views must already reflect it; Verify proves indexes == full re-derive.
func TestMultiViewAtomicVisibility(t *testing.T) {
	types, views := testSchema()
	s := NewStore(types, views)

	w, err := s.Write(WriteRequest{Type: "Order", Key: "o1", Attrs: order("east", "book", 100), Expected: 0})
	if err != nil {
		t.Fatal(err)
	}
	logLine(t, "INPUT write o1 region=east category=book amount=100 expected=0")
	logLine(t, "OUTPUT version=%d seq=%d; BASIS commit effective point flips all views together", w.Version, w.Sequence)

	assertGroup(t, s, "AmountByRegion", "east", 100, 1)
	assertGroup(t, s, "AmountByCategory", "book", 100, 1)
	assertGroup(t, s, "CountByRegion", "east", 1, 1)
	logLine(t, "BASIS all three views visible simultaneously after one commit")

	if err := s.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// TestGroupMigrationMutualExclusion: changing the region key must deduct the
// old group and credit the new group as one indivisible event.
func TestGroupMigrationMutualExclusion(t *testing.T) {
	types, views := testSchema()
	s := NewStore(types, views)

	w1, _ := s.Write(WriteRequest{Type: "Order", Key: "o1", Attrs: order("east", "book", 100), Expected: 0})
	w2, err := s.Write(WriteRequest{Type: "Order", Key: "o1", Attrs: order("west", "book", 150), Expected: w1.Version})
	if err != nil {
		t.Fatal(err)
	}
	logLine(t, "INPUT migrate o1 east->west amount 100->150 expected=%d", w1.Version)
	logLine(t, "OUTPUT version=%d; BASIS old group withdraw + new group add in one critical section", w2.Version)

	assertEmpty(t, s, "AmountByRegion", "east")
	assertEmpty(t, s, "CountByRegion", "east")
	assertGroup(t, s, "AmountByRegion", "west", 150, 1)
	assertGroup(t, s, "CountByRegion", "west", 1, 1)
	assertGroup(t, s, "AmountByCategory", "book", 150, 1)
	logLine(t, "BASIS o1 exists exactly once in region space (west), never in both/neither")

	if err := s.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// TestDeleteSemantics distinguishes never-committed delete (NOT_FOUND) from
// delete-again-after-delete (VERSION_CONFLICT/ALREADY_DELETED), and checks
// aggregate contributions are withdrawn at the delete's effective point.
func TestDeleteSemantics(t *testing.T) {
	types, views := testSchema()
	s := NewStore(types, views)

	logLine(t, "INPUT delete ghost (never committed) expected=0")
	_, err := s.Delete(DeleteRequest{Type: "Order", Key: "ghost", Expected: 0})
	oe := mustCode(t, err, ErrNotFound)
	logLine(t, "OUTPUT %s; BASIS primary key never committed a version", oe.Code)

	w, _ := s.Write(WriteRequest{Type: "Order", Key: "o1", Attrs: order("east", "book", 100), Expected: 0})
	assertGroup(t, s, "AmountByRegion", "east", 100, 1)

	if _, err := s.Delete(DeleteRequest{Type: "Order", Key: "o1", Expected: w.Version}); err != nil {
		t.Fatal(err)
	}
	logLine(t, "INPUT delete o1 expected=%d; OUTPUT committed; BASIS contributions withdrawn now", w.Version)
	assertEmpty(t, s, "AmountByRegion", "east")
	assertEmpty(t, s, "AmountByCategory", "book")
	assertEmpty(t, s, "CountByRegion", "east")

	_, err = s.Delete(DeleteRequest{Type: "Order", Key: "o1", Expected: 0})
	oe = mustCode(t, err, ErrVersionConflict)
	if oe.Reason != ReasonAlreadyDeleted {
		t.Fatalf("want reason %s, got %s", ReasonAlreadyDeleted, oe.Reason)
	}
	logLine(t, "INPUT delete o1 again; OUTPUT %s/%s; BASIS distinct from NOT_FOUND", oe.Code, oe.Reason)

	if _, ok := s.Get("Order", "o1"); ok {
		t.Fatal("deleted instance must not be readable as live")
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// TestErrorPriorityOrdering verifies only the first-hit reason is reported:
// invalid argument > version conflict > not-found.
func TestErrorPriorityOrdering(t *testing.T) {
	types, views := testSchema()
	s := NewStore(types, views)

	// Empty primary key AND stale token: invalid argument wins.
	_, err := s.Write(WriteRequest{Type: "Order", Key: "", Attrs: order("east", "book", 1), Expected: 99})
	mustCode(t, err, ErrInvalidArgument)
	logLine(t, "INPUT write key='' expected=99; OUTPUT INVALID; BASIS empty key precedes token check")

	// Type mismatch AND stale token: invalid wins.
	w, _ := s.Write(WriteRequest{Type: "Order", Key: "o1", Attrs: order("east", "book", 10), Expected: 0})
	bad := map[string]any{"region": "east", "category": "book", "amount": "notint"}
	_, err = s.Write(WriteRequest{Type: "Order", Key: "o1", Attrs: bad, Expected: 99})
	mustCode(t, err, ErrInvalidArgument)
	logLine(t, "INPUT amount=string expected=99; OUTPUT INVALID; BASIS type mismatch precedes token")

	// Delete with empty key on a never-committed key: invalid beats not-found.
	_, err = s.Delete(DeleteRequest{Type: "Order", Key: "", Expected: 0})
	mustCode(t, err, ErrInvalidArgument)
	logLine(t, "INPUT delete key=''; OUTPUT INVALID; BASIS invalid precedes not-found")

	// Stale token on a live record => conflict.
	_, err = s.Write(WriteRequest{Type: "Order", Key: "o1", Attrs: order("east", "book", 11), Expected: 99})
	oe := mustCode(t, err, ErrVersionConflict)
	if oe.Reason != ReasonStaleVersion {
		t.Fatalf("want %s got %s", ReasonStaleVersion, oe.Reason)
	}
	_ = w
}

// TestRejectedOpConsumesNoVersion: a rejected credential must not skip a
// version number and must leave every aggregate untouched.
func TestRejectedOpConsumesNoVersion(t *testing.T) {
	types, views := testSchema()
	s := NewStore(types, views)
	w1, _ := s.Write(WriteRequest{Type: "Order", Key: "o1", Attrs: order("east", "book", 10), Expected: 0})

	if _, err := s.Write(WriteRequest{Type: "Order", Key: "o1", Attrs: order("east", "book", 20), Expected: 99}); err == nil {
		t.Fatal("expected conflict")
	}
	w2, err := s.Write(WriteRequest{Type: "Order", Key: "o1", Attrs: order("east", "book", 20), Expected: w1.Version})
	if err != nil {
		t.Fatal(err)
	}
	if w2.Version != w1.Version+1 {
		t.Fatalf("rejected op consumed a version: %d -> %d", w1.Version, w2.Version)
	}
	assertGroup(t, s, "AmountByRegion", "east", 20, 1)
	logLine(t, "BASIS rejected expected=99 left version at %d; next commit = %d", w1.Version, w2.Version)
}

// TestOptimisticInterleaving models two clients racing on one instance.
func TestOptimisticInterleaving(t *testing.T) {
	types, views := testSchema()
	s := NewStore(types, views)
	base, _ := s.Write(WriteRequest{Type: "Order", Key: "o1", Attrs: order("east", "book", 1), Expected: 0})

	_, errA := s.Write(WriteRequest{Type: "Order", Key: "o1", Attrs: order("east", "book", 2), Expected: base.Version})
	if errA != nil {
		t.Fatal(errA)
	}
	_, errB := s.Write(WriteRequest{Type: "Order", Key: "o1", Attrs: order("west", "book", 3), Expected: base.Version})
	oe := mustCode(t, errB, ErrVersionConflict)
	if oe.Reason != ReasonStaleVersion {
		t.Fatalf("want stale, got %s", oe.Reason)
	}
	logLine(t, "BASIS B's stale credential %d rejected after A committed", base.Version)

	got, _ := s.Get("Order", "o1")
	retry, err := s.Write(WriteRequest{Type: "Order", Key: "o1", Attrs: order("west", "book", 3), Expected: got.Version})
	if err != nil {
		t.Fatal(err)
	}
	if retry.Version != base.Version+2 {
		t.Fatalf("want final version %d got %d", base.Version+2, retry.Version)
	}
	assertGroup(t, s, "AmountByRegion", "west", 3, 1)
	assertEmpty(t, s, "AmountByRegion", "east")
}

// TestReplayDeterminism replays the accepted journal onto an empty store and
// requires identical aggregate values.
func TestReplayDeterminism(t *testing.T) {
	types, views := testSchema()
	s := NewStore(types, views)
	s.Write(WriteRequest{Type: "Order", Key: "o1", Attrs: order("east", "book", 100), Expected: 0})
	w2, _ := s.Write(WriteRequest{Type: "Order", Key: "o1", Attrs: order("west", "book", 100), Expected: 1})
	s.Write(WriteRequest{Type: "Order", Key: "o2", Attrs: order("east", "toy", 50), Expected: 0})
	s.Delete(DeleteRequest{Type: "Order", Key: "o2", Expected: 1})
	s.Write(WriteRequest{Type: "Order", Key: "o3", Attrs: order("east", "toy", 25), Expected: 0})
	_ = w2

	j := s.Journal()
	r := Replay(types, views, j)
	for _, view := range views {
		for _, g := range r.maintainer.groupNames(view.Name) {
			a := s.Query(view.Name, g)
			b := r.Query(view.Name, g)
			if a != b {
				t.Fatalf("replay mismatch %s[%s]: %+v vs %+v", view.Name, g, a, b)
			}
		}
	}
	if err := r.Verify(); err != nil {
		t.Fatalf("replay verify: %v", err)
	}
	logLine(t, "BASIS replaying %d accepted commits reproduces every aggregate", len(j))
}
