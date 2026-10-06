package cg

import "testing"

// Queue exactly full succeeds; the overflow write is discarded with
// QueueFull and consumes no sequence number.
func TestQueueFullAndOverflow(t *testing.T) {
	c := newTestCoordinator(t, 100, 2)
	mustCreate(t, c, 0, "g", "a", "b")
	if _, err := c.BeginSnapshot(0, "g", 50); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ConfirmFreeze(1, "g", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ConfirmFreeze(1, "g", "b"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if r, err := c.Write(2, "a", "q"); err != nil || !r.Queued {
			t.Fatalf("fill write %d: %+v %v", i, r, err)
		}
	}
	if _, err := c.Write(2, "a", "overflow"); kindOf(err) != QueueFull {
		t.Fatalf("overflow must be queue_full, got %v", err)
	}
	if err := c.Abort(3, "g"); err != nil {
		t.Fatal(err)
	}
	va, _ := c.GetVolume(3, "a")
	if va.Seq != 2 {
		t.Fatalf("only 2 queued writes may consume seq, got %d", va.Seq)
	}
	if r, err := c.Write(4, "a", "after"); err != nil || r.Seq != 3 {
		t.Fatalf("discarded write must not occupy seq; next seq 3, got %+v %v", r, err)
	}
}

// Auto-abort followed immediately by a fresh begin works.
func TestAutoAbortThenRebegin(t *testing.T) {
	c := newTestCoordinator(t, 100, 4)
	mustCreate(t, c, 0, "g", "a", "b")
	id1, err := c.BeginSnapshot(0, "g", 10)
	if err != nil {
		t.Fatal(err)
	}
	// A write at t=11 triggers the freezing timeout auto-abort first, then
	// applies normally because the group is idle.
	if r, err := c.Write(11, "a", "x"); err != nil || r.Seq != 1 {
		t.Fatalf("write after auto-abort must apply as seq=1, got %+v %v", r, err)
	}
	id2, err := c.BeginSnapshot(11, "g", 20)
	if err != nil {
		t.Fatalf("rebegin right after auto-abort: %v", err)
	}
	if id2 != id1+1 {
		t.Fatalf("snapshot ids must advance %d -> %d", id1, id2)
	}
	if _, err := c.ConfirmFreeze(11, "g", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ConfirmFreeze(11, "g", "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Commit(11, "g"); err != nil {
		t.Fatal(err)
	}
}

// Commit drains queued writes in per-volume arrival order and assigns
// contiguous sequence numbers from each volume cutoff onward.
func TestCommitDrainSequences(t *testing.T) {
	c := newTestCoordinator(t, 100, 10)
	mustCreate(t, c, 0, "g", "a", "b")
	if _, err := c.Write(0, "a", "0"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.BeginSnapshot(1, "g", 50); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ConfirmFreeze(2, "g", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(3, "a", "q1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(3, "b", "b1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ConfirmFreeze(4, "g", "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(5, "a", "q2"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(5, "b", "b2"); err != nil {
		t.Fatal(err)
	}
	rec, err := c.Commit(6, "g")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Cutoffs["a"] != 1 || rec.Cutoffs["b"] != 1 {
		t.Fatalf("cutoffs: %+v", rec.Cutoffs)
	}
	va, _ := c.GetVolume(6, "a")
	vb, _ := c.GetVolume(6, "b")
	if va.Seq != 3 || vb.Seq != 2 {
		t.Fatalf("drained seqs a=%d b=%d, want 3/2", va.Seq, vb.Seq)
	}
	if rec2, _ := c.LastSnapshot(6, "g"); rec2 == nil || rec2.ID != rec.ID {
		t.Fatal("committed record must be retrievable")
	}
}

// Clock regression and operational rejections leave no domain trace.
func TestRejectionsLeaveNoTrace(t *testing.T) {
	c := newTestCoordinator(t, 100, 2)
	mustCreate(t, c, 10, "g", "a", "b")
	if err := c.CreateGroup(10, GroupSpec{GroupID: "bad", VolumeIDs: []string{"x"}}); kindOf(err) != InvalidArgument {
		t.Fatalf("size<2: %v", err)
	}
	if err := c.CreateGroup(10, GroupSpec{GroupID: "bad2", VolumeIDs: []string{"x", "x"}}); kindOf(err) != InvalidArgument {
		t.Fatalf("duplicate members: %v", err)
	}
	if _, err := c.Write(9, "a", "x"); kindOf(err) != ClockBackward {
		t.Fatalf("clock backward: %v", err)
	}
	if _, err := c.Write(10, "a", "x"); err != nil {
		t.Fatalf("t=10 must still be usable: %v", err)
	}
	if _, err := c.Write(10, "zzz", "x"); kindOf(err) != NotFound {
		t.Fatal("missing volume must be not_found")
	}
	if err := c.CreateGroup(10, GroupSpec{GroupID: "g2", VolumeIDs: []string{"a", "z"}}); kindOf(err) != Conflict {
		t.Fatalf("volume already grouped -> conflict: %v", err)
	}
	if _, err := c.BeginSnapshot(10, "g", 50); err != nil {
		t.Fatal(err)
	}
	if _, err := c.BeginSnapshot(10, "g", 50); kindOf(err) != StateError {
		t.Fatalf("second begin -> state_error: %v", err)
	}
	if _, err := c.Commit(10, "g"); kindOf(err) != StateError {
		t.Fatalf("commit while freezing -> state_error: %v", err)
	}
	// Nothing applied after seq=1: queued nothing, rejects left no trace.
	va, _ := c.GetVolume(10, "a")
	vb, _ := c.GetVolume(10, "b")
	if va.Seq != 1 || vb.Seq != 0 {
		t.Fatalf("rejects changed state: a=%d b=%d", va.Seq, vb.Seq)
	}
}

// Member changes are rejected while a snapshot is in progress, and allowed
// once idle; group conflicts are whole-request rejections.
func TestMemberChangesBlockedDuringSnapshot(t *testing.T) {
	c := newTestCoordinator(t, 100, 4)
	mustCreate(t, c, 0, "g", "a", "b")
	if _, err := c.BeginSnapshot(0, "g", 50); err != nil {
		t.Fatal(err)
	}
	if err := c.AddMembers(0, "g", []string{"c"}); kindOf(err) != StateError {
		t.Fatalf("add during snapshot: %v", err)
	}
	if err := c.RemoveMembers(0, "g", []string{"b"}); kindOf(err) != StateError {
		t.Fatalf("remove during snapshot: %v", err)
	}
	if err := c.Abort(1, "g"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddMembers(1, "g", []string{"c"}); err != nil {
		t.Fatalf("add while idle: %v", err)
	}
	st, _ := c.GetGroup(1, "g")
	if len(st.Members) != 3 {
		t.Fatalf("want 3 members, got %d", len(st.Members))
	}
	// Conflict: one volume from another group rejects the whole add.
	mustCreate(t, c, 2, "g2", "x", "y")
	if err := c.AddMembers(2, "g", []string{"z", "x"}); kindOf(err) != Conflict {
		t.Fatalf("partial conflict must reject all: %v", err)
	}
	st, _ = c.GetGroup(2, "g")
	if len(st.Members) != 3 {
		t.Fatalf("conflicting add must be atomic, got %d members", len(st.Members))
	}
	if err := c.RemoveMembers(2, "g", []string{"a", "b", "c"}); kindOf(err) != InvalidArgument {
		t.Fatalf("removing below 2 volumes is invalid_argument: %v", err)
	}
}

// Ungrouped volumes (here: volumes only exist through groups, so check
// groups in idle state) always apply writes directly.
func TestIdleGroupWritesApply(t *testing.T) {
	c := newTestCoordinator(t, 100, 4)
	mustCreate(t, c, 0, "g", "a", "b")
	r, err := c.Write(0, "a", "w")
	if err != nil || r.Queued || r.Seq != 1 {
		t.Fatalf("idle group write: %+v %v", r, err)
	}
	if err := c.Abort(0, "g"); kindOf(err) != StateError {
		t.Fatalf("abort with no snapshot -> state_error: %v", err)
	}
}
