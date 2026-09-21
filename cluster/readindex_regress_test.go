package cluster

import "testing"

// Regression: ReadIndex must require a reachable MAJORITY, not just
// one reachable replica; the old check (reachable < 1) happily
// returned a stale minority view.
func TestReadIndexMinorityReachableFails(t *testing.T) {
	c := New(5, nil)
	if _, committed, err := c.Propose(1, "x"); err != nil || !committed {
		t.Fatalf("Propose = (_, %v, %v)", committed, err)
	}
	// Isolate all but one replica: only a minority stays reachable.
	for id := 1; id < 5; id++ {
		c.SetFault(id, FaultDrop, 0)
	}
	if _, err := c.ReadIndex(); err == nil {
		t.Fatal("ReadIndex succeeded with only 1 of 5 reachable")
	}
	// Healing back to an exact majority must make it succeed again.
	c.SetFault(1, FaultNone, 0)
	c.SetFault(2, FaultNone, 0)
	ri, err := c.ReadIndex()
	if err != nil {
		t.Fatalf("ReadIndex with 3 of 5 reachable: %v", err)
	}
	if ri < c.Commit() {
		t.Fatalf("ReadIndex = %d, below commit %d", ri, c.Commit())
	}
}
