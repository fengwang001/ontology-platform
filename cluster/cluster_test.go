package cluster

import (
	"reflect"
	"testing"
	"time"
)

func newCluster(n int) *Cluster {
	return New(n, time.Now)
}

func mustPropose(t *testing.T, c *Cluster, term uint64, data string) (uint64, bool) {
	t.Helper()
	idx, committed, err := c.Propose(term, data)
	if err != nil {
		t.Fatalf("Propose(%d, %q): %v", term, data, err)
	}
	return idx, committed
}

func TestCommittedEntriesNeverLost(t *testing.T) {
	c := newCluster(3)
	var lastCommit uint64
	for i, d := range []string{"a", "b", "c"} {
		idx, committed := mustPropose(t, c, 1, d)
		if !committed {
			t.Fatalf("entry %d not committed", idx)
		}
		if c.Commit() < lastCommit {
			t.Fatal("commit regressed")
		}
		lastCommit = c.Commit()
		_ = i
	}
	if c.Commit() != 3 {
		t.Fatalf("Commit() = %d, want 3", c.Commit())
	}
	// Inject faults and keep proposing; committed prefix must survive.
	c.SetFault(1, FaultReject, 0)
	c.SetFault(2, FaultDrop, 0)
	if _, committed := mustPropose(t, c, 2, "d"); committed {
		t.Fatal("entry d must not commit without a majority")
	}
	c.SetFault(1, FaultLag, 1)
	mustPropose(t, c, 2, "e")
	for id := 0; id < 3; id++ {
		snap := c.Snapshot(id)
		if len(snap) < 3 || !reflect.DeepEqual(snap[:3], []string{"a", "b", "c"}) {
			t.Fatalf("replica %d lost committed prefix: %v", id, snap)
		}
	}
	if c.Commit() < 3 {
		t.Fatalf("Commit() = %d, want >= 3", c.Commit())
	}
}

func TestNoQuorumNoCommit(t *testing.T) {
	c := newCluster(5)
	mustPropose(t, c, 1, "ok")
	if c.Commit() != 1 {
		t.Fatalf("Commit() = %d, want 1", c.Commit())
	}
	// Knock out 3 of 5 replicas: no majority possible.
	c.SetFault(2, FaultReject, 0)
	c.SetFault(3, FaultDrop, 0)
	c.SetFault(4, FaultReject, 0)
	idx, committed := mustPropose(t, c, 1, "stuck")
	if committed {
		t.Fatal("must not commit without majority")
	}
	if c.Commit() != 1 {
		t.Fatalf("Commit() = %d, want 1", c.Commit())
	}
	for id := 0; id < 5; id++ {
		if snap := c.Snapshot(id); !reflect.DeepEqual(snap, []string{"ok"}) {
			t.Fatalf("replica %d snapshot = %v, want [ok]", id, snap)
		}
	}
	_ = idx
}

func TestLagReplicaCatchesUp(t *testing.T) {
	c := newCluster(3)
	c.SetFault(2, FaultLag, 2)
	for i := 1; i <= 5; i++ {
		mustPropose(t, c, 1, string(rune('a'+i-1)))
	}
	if snap := c.Snapshot(2); len(snap) != 3 {
		t.Fatalf("lagging replica snapshot = %v, want 3 entries", snap)
	}
	c.SetFault(2, FaultNone, 0)
	mustPropose(t, c, 1, "f")
	want := c.Snapshot(0)
	for id := 1; id < 3; id++ {
		if got := c.Snapshot(id); !reflect.DeepEqual(got, want) {
			t.Fatalf("replica %d snapshot = %v, want %v", id, got, want)
		}
	}
	if len(want) != 6 {
		t.Fatalf("snapshot = %v, want 6 entries", want)
	}
}

func TestReadIndex(t *testing.T) {
	c := newCluster(3)
	mustPropose(t, c, 1, "a")
	mustPropose(t, c, 1, "b")
	idx, err := c.ReadIndex()
	if err != nil {
		t.Fatalf("ReadIndex: %v", err)
	}
	if idx < c.Commit() {
		t.Fatalf("ReadIndex = %d < Commit = %d", idx, c.Commit())
	}
	// Lagging replicas still count as reachable.
	c.SetFault(1, FaultLag, 1)
	c.SetFault(2, FaultLag, 2)
	if _, err := c.ReadIndex(); err != nil {
		t.Fatalf("ReadIndex with lagging majority: %v", err)
	}
	// Losing a majority must fail instead of returning a stale index.
	c.SetFault(1, FaultReject, 0)
	c.SetFault(2, FaultDrop, 0)
	if _, err := c.ReadIndex(); err == nil {
		t.Fatal("ReadIndex must fail without a reachable majority")
	}
}
