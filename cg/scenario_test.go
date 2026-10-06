package cg

import "testing"

// Confirmation exactly at the deadline is valid; one tick later auto-aborts.
func TestFreezeDeadlineBoundary(t *testing.T) {
	c := newTestCoordinator(t, 100, 4)
	mustCreate(t, c, 0, "g", "a", "b")
	if _, err := c.BeginSnapshot(10, "g", 20); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ConfirmFreeze(20, "g", "a"); err != nil {
		t.Fatalf("confirm at deadline: %v", err)
	}
	last, err := c.ConfirmFreeze(20, "g", "b")
	if err != nil || !last {
		t.Fatalf("last confirm at deadline: last=%v err=%v", last, err)
	}
	if _, err := c.Commit(20, "g"); err != nil {
		t.Fatalf("commit at deadline: %v", err)
	}

	// New snapshot: one volume confirmed at 20; any op strictly after 20
	// with confirmation incomplete auto-aborts first.
	if _, err := c.BeginSnapshot(20, "g", 20); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ConfirmFreeze(20, "g", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ConfirmFreeze(21, "g", "b"); kindOf(err) != StateError {
		t.Fatalf("want state_error after auto-abort, got %v", err)
	}
	st, _ := c.GetGroup(21, "g")
	if st.Phase != Idle {
		t.Fatalf("group must be idle after auto-abort, got %s", st.Phase)
	}
	if rec, _ := c.LastSnapshot(21, "g"); rec == nil || rec.ID != 1 {
		t.Fatalf("abort must not erase the earlier committed record, got %+v", rec)
	}
}

// Commit exactly at point+hold is valid; strictly after auto-aborts.
func TestHoldBoundary(t *testing.T) {
	c := newTestCoordinator(t, 5, 4)
	mustCreate(t, c, 0, "g", "a", "b")
	if _, err := c.BeginSnapshot(0, "g", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ConfirmFreeze(10, "g", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ConfirmFreeze(10, "g", "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Commit(16, "g"); kindOf(err) != StateError {
		t.Fatalf("commit past hold must auto-abort then state_error, got %v", err)
	}

	mustCreate(t, c, 20, "g2", "c", "d")
	if _, err := c.BeginSnapshot(20, "g2", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ConfirmFreeze(20, "g2", "c"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ConfirmFreeze(20, "g2", "d"); err != nil {
		t.Fatal(err)
	}
	// point=20, hold=5 => commit at 25 is exactly on the boundary.
	if _, err := c.Commit(25, "g2"); err != nil {
		t.Fatalf("commit at point+hold boundary: %v", err)
	}
}

// last=true only for the final confirmation; a duplicate confirmation is
// rejected and consumes no slot.
func TestLastConfirmationAndDuplicate(t *testing.T) {
	c := newTestCoordinator(t, 100, 4)
	mustCreate(t, c, 0, "g", "a", "b", "c")
	if _, err := c.BeginSnapshot(0, "g", 50); err != nil {
		t.Fatal(err)
	}
	last, err := c.ConfirmFreeze(1, "g", "a")
	if err != nil || last {
		t.Fatalf("first confirm last=%v err=%v", last, err)
	}
	if _, err := c.ConfirmFreeze(2, "g", "a"); kindOf(err) != DuplicateConfirm {
		t.Fatalf("duplicate confirm, got %v", err)
	}
	last, err = c.ConfirmFreeze(3, "g", "b")
	if err != nil || last {
		t.Fatalf("second distinct confirm last=%v err=%v", last, err)
	}
	last, err = c.ConfirmFreeze(4, "g", "c")
	if err != nil || !last {
		t.Fatalf("third confirm must be last, got %v %v", last, err)
	}
}

// During freezing, confirmed volumes queue writes while unconfirmed volumes
// apply them; after the last confirmation all writes queue.
func TestFreezingWriteDifferences(t *testing.T) {
	c := newTestCoordinator(t, 100, 10)
	mustCreate(t, c, 0, "g", "a", "b")
	for i := 0; i < 3; i++ {
		if _, err := c.Write(0, "a", "x"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.BeginSnapshot(1, "g", 50); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ConfirmFreeze(2, "g", "a"); err != nil {
		t.Fatal(err)
	}
	if r, err := c.Write(3, "a", "w"); err != nil || !r.Queued || r.Seq != 0 {
		t.Fatalf("confirmed-volume write must queue, got %+v %v", r, err)
	}
	if r, err := c.Write(3, "b", "w"); err != nil || r.Queued || r.Seq != 1 {
		t.Fatalf("unconfirmed-volume write applies at seq=1, got %+v %v", r, err)
	}
	if _, err := c.ConfirmFreeze(4, "g", "b"); err != nil {
		t.Fatal(err)
	}
	rec, err := c.Commit(5, "g")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Cutoffs["a"] != 3 || rec.Cutoffs["b"] != 1 || rec.Point != 4 {
		t.Fatalf("bad cutoffs/point: %+v", rec)
	}
	va, _ := c.GetVolume(5, "a")
	vb, _ := c.GetVolume(5, "b")
	// a's queued write drains as seq=4; b has no queued writes.
	if va.Seq != 4 || vb.Seq != 1 {
		t.Fatalf("post-commit seqs a=%d b=%d", va.Seq, vb.Seq)
	}
}
