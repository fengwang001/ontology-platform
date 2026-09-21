package cluster

import "testing"

// Regression: ReadIndex only checked reachable >= 1, so it returned a
// stale commit index when just a minority of replicas was reachable;
// it must require a reachable majority.
func TestReadIndexMinorityUnreachableRegression(t *testing.T) {
	c := New(5, nil)
	if _, committed, _ := c.Propose(1, "x"); !committed {
		t.Fatal("expected commit on healthy cluster")
	}
	// Knock out 3 of 5 replicas: only a minority remains reachable.
	c.SetFault(2, FaultDrop, 0)
	c.SetFault(3, FaultReject, 0)
	c.SetFault(4, FaultDrop, 0)
	if _, err := c.ReadIndex(); err == nil {
		t.Fatal("ReadIndex must fail with only 2 of 5 replicas reachable")
	}
	// Healing one replica restores a majority and the read succeeds.
	c.SetFault(2, FaultNone, 0)
	ri, err := c.ReadIndex()
	if err != nil || ri < c.Commit() {
		t.Fatalf("ReadIndex = (%d, %v), commit = %d", ri, err, c.Commit())
	}
}
