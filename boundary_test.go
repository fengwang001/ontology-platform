package coordinator

import (
	"errors"
	"testing"
)

func appendCommitApply(t *testing.T, c *Coordinator, n int) {
	t.Helper()
	for range n {
		if _, err := c.Append(1); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	if err := c.Commit(n); err != nil {
		t.Fatalf("Commit(%d) error = %v", n, err)
	}
}

func TestSnapshotThresholdEquality(t *testing.T) {
	c, _ := New(0, 3, 0)
	appendCommitApply(t, c, 3)

	took, err := c.Apply(2)
	if err != nil || took {
		t.Fatalf("Apply(2) = (%v,%v), want false,nil", took, err)
	}
	if c.snapIndex != 0 || c.baseIndex != 0 {
		t.Fatalf("state = snap %d base %d, want 0,0", c.snapIndex, c.baseIndex)
	}
	took, err = c.Apply(3)
	if err != nil || !took {
		t.Fatalf("Apply(3) = (%v,%v), want true,nil", took, err)
	}
	if c.snapIndex != 3 || c.baseIndex != 3 {
		t.Fatalf("state = snap %d base %d, want 3,3", c.snapIndex, c.baseIndex)
	}
}

func TestTailRetentionAndZeroTail(t *testing.T) {
	largeTail, _ := New(5, 1, 0)
	appendCommitApply(t, largeTail, 3)
	if _, err := largeTail.Apply(3); err != nil {
		t.Fatalf("Apply(3) error = %v", err)
	}
	if largeTail.baseIndex != 0 {
		t.Fatalf("baseIndex = %d, want 0 when tail exceeds snapshot", largeTail.baseIndex)
	}

	zeroTail, _ := New(0, 1, 0)
	appendCommitApply(t, zeroTail, 2)
	if _, err := zeroTail.Apply(1); err != nil {
		t.Fatalf("Apply(1) error = %v", err)
	}
	if zeroTail.snapIndex != 1 || zeroTail.baseIndex != 1 || zeroTail.removed != 1 {
		t.Fatalf("state = snap %d base %d removed %d, want 1,1,1", zeroTail.snapIndex, zeroTail.baseIndex, zeroTail.removed)
	}
}

func TestPlanBoundaryAndBaseTerm(t *testing.T) {
	c, _ := New(2, 4, 0)
	appendCommitApply(t, c, 4)
	if _, err := c.Apply(4); err != nil {
		t.Fatalf("Apply(4) error = %v", err)
	}
	if err := c.AddPeer("p"); err != nil {
		t.Fatalf("AddPeer(p) error = %v", err)
	}

	if err := c.Retreat("p", 3); err != nil {
		t.Fatalf("Retreat(p, 3) error = %v", err)
	}
	assertPlan(t, c, "p", Plan{Kind: AppendEntries, PrevIndex: 2, PrevTerm: 1, From: 3, To: 4})

	if err := c.Retreat("p", 2); err != nil {
		t.Fatalf("Retreat(p, 2) error = %v", err)
	}
	assertPlan(t, c, "p", Plan{Kind: NeedSnapshot, Snapshot: 4, SnapTerm: 1})
}

func TestInFlightSnapshotFixesCut(t *testing.T) {
	c, _ := New(0, 2, 0)
	appendCommitApply(t, c, 2)
	if _, err := c.Apply(2); err != nil {
		t.Fatalf("Apply(2) error = %v", err)
	}
	if err := c.AddPeer("p"); err != nil {
		t.Fatalf("AddPeer(p) error = %v", err)
	}
	if err := c.Retreat("p", 1); err != nil {
		t.Fatalf("Retreat(p, 1) error = %v", err)
	}
	if _, _, err := c.StartSnapshot("p"); err != nil {
		t.Fatalf("StartSnapshot(p) error = %v", err)
	}

	if _, err := c.Append(2); err != nil {
		t.Fatalf("Append(2) error = %v", err)
	}
	if _, err := c.Append(2); err != nil {
		t.Fatalf("Append(second 2) error = %v", err)
	}
	if err := c.Commit(4); err != nil {
		t.Fatalf("Commit(4) error = %v", err)
	}
	if _, err := c.Apply(4); err != nil {
		t.Fatalf("Apply(4) error = %v", err)
	}
	if c.snapIndex != 4 || c.baseIndex != 2 {
		t.Fatalf("state = snap %d base %d, want snap 4 fixed base 2", c.snapIndex, c.baseIndex)
	}
}

func TestLagProtectionEquality(t *testing.T) {
	protected, _ := New(0, 3, 3)
	if err := protected.AddPeer("p"); err != nil {
		t.Fatalf("AddPeer(p) error = %v", err)
	}
	appendCommitApply(t, protected, 3)
	if _, err := protected.Apply(3); err != nil {
		t.Fatalf("Apply(3) error = %v", err)
	}
	if protected.baseIndex != 0 {
		t.Fatalf("equal lag baseIndex = %d, want protection at 0", protected.baseIndex)
	}

	unprotected, _ := New(0, 4, 3)
	if err := unprotected.AddPeer("q"); err != nil {
		t.Fatalf("AddPeer(q) error = %v", err)
	}
	appendCommitApply(t, unprotected, 4)
	if _, err := unprotected.Apply(4); err != nil {
		t.Fatalf("Apply(4) error = %v", err)
	}
	if unprotected.baseIndex != 4 {
		t.Fatalf("lag one beyond limit baseIndex = %d, want 4", unprotected.baseIndex)
	}
}

func TestPeerRejectionDoesNotChangeState(t *testing.T) {
	c, _ := New(0, 1, 0)
	appendCommitApply(t, c, 1)
	if _, err := c.Apply(1); err != nil {
		t.Fatalf("Apply(1) error = %v", err)
	}
	if err := c.AddPeer("p"); err != nil {
		t.Fatalf("AddPeer(p) error = %v", err)
	}

	before := *c.peers["p"]
	for _, action := range []func() error{
		func() error { return c.AddPeer("p") },
		func() error { return c.Retreat("p", 0) },
		func() error { return c.Retreat("p", 3) },
		func() error { return c.Ack("p", -1) },
		func() error { return c.FinishSnapshot("p") },
		func() error { return c.AbortSnapshot("p") },
	} {
		if err := action(); err == nil {
			t.Fatalf("invalid action succeeded; peer=%+v", *c.peers["p"])
		}
		if got := *c.peers["p"]; got != before {
			t.Fatalf("peer changed after rejection: %+v, want %+v", got, before)
		}
	}

	if err := c.Ack("p", 1); err != nil {
		t.Fatalf("Ack(p, 1) error = %v", err)
	}
	if err := c.Ack("p", 0); !errors.Is(err, ErrRange) {
		t.Fatalf("non-monotonic Ack error = %v, want ErrRange", err)
	}
}

func TestAbortAndDropReleaseSnapshotFixPoint(t *testing.T) {
	aborted, _ := New(0, 1, 0)
	appendCommitApply(t, aborted, 1)
	if _, err := aborted.Apply(1); err != nil {
		t.Fatalf("Apply(1) error = %v", err)
	}
	if err := aborted.AddPeer("p"); err != nil {
		t.Fatalf("AddPeer(p) error = %v", err)
	}
	if err := aborted.Retreat("p", 1); err != nil {
		t.Fatalf("Retreat(p, 1) error = %v", err)
	}
	if _, _, err := aborted.StartSnapshot("p"); err != nil {
		t.Fatalf("StartSnapshot(p) error = %v", err)
	}
	if _, err := aborted.Append(2); err != nil {
		t.Fatalf("Append(2) error = %v", err)
	}
	if err := aborted.Commit(2); err != nil {
		t.Fatalf("Commit(2) error = %v", err)
	}
	if _, err := aborted.Apply(2); err != nil {
		t.Fatalf("Apply(2) error = %v", err)
	}
	if aborted.baseIndex != 1 {
		t.Fatalf("baseIndex while in flight = %d, want fixed at 1", aborted.baseIndex)
	}
	if err := aborted.AbortSnapshot("p"); err != nil {
		t.Fatalf("AbortSnapshot(p) error = %v", err)
	}
	if aborted.baseIndex != 2 || aborted.peers["p"].match != 0 || aborted.peers["p"].next != 1 {
		t.Fatalf("after abort base=%d peer=(%d,%d), want base=2 peer=(0,1)",
			aborted.baseIndex, aborted.peers["p"].match, aborted.peers["p"].next)
	}

	dropped, _ := New(0, 1, 0)
	appendCommitApply(t, dropped, 1)
	if _, err := dropped.Apply(1); err != nil {
		t.Fatalf("Apply(1) error = %v", err)
	}
	if err := dropped.AddPeer("q"); err != nil {
		t.Fatalf("AddPeer(q) error = %v", err)
	}
	if err := dropped.Retreat("q", 1); err != nil {
		t.Fatalf("Retreat(q, 1) error = %v", err)
	}
	if _, _, err := dropped.StartSnapshot("q"); err != nil {
		t.Fatalf("StartSnapshot(q) error = %v", err)
	}
	if _, err := dropped.Append(2); err != nil {
		t.Fatalf("Append(2) error = %v", err)
	}
	if err := dropped.Commit(2); err != nil {
		t.Fatalf("Commit(2) error = %v", err)
	}
	if _, err := dropped.Apply(2); err != nil {
		t.Fatalf("Apply(2) error = %v", err)
	}
	if dropped.baseIndex != 1 {
		t.Fatalf("baseIndex before drop = %d, want 1", dropped.baseIndex)
	}
	if err := dropped.DropPeer("q"); err != nil {
		t.Fatalf("DropPeer(q) error = %v", err)
	}
	if dropped.baseIndex != 2 {
		t.Fatalf("baseIndex after drop = %d, want 2", dropped.baseIndex)
	}
}
