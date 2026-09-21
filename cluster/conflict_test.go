package cluster

import (
	"reflect"
	"testing"

	"ontology/replica"
)

// TestConflictTruncateRepair manufactures a divergent uncommitted tail on
// one replica, then proves the gateway truncates it and re-replicates so
// that every committed snapshot is a prefix of every other.
func TestConflictTruncateRepair(t *testing.T) {
	c := newCluster(5)
	mustPropose(t, c, 1, "e1") // commits on all five
	// Isolate three replicas so e2 lands only on replicas 0 and 1.
	for id := 2; id < 5; id++ {
		c.SetFault(id, FaultReject, 0)
	}
	if _, committed := mustPropose(t, c, 2, "e2"); committed {
		t.Fatal("e2 must not commit on 2 of 5 replicas")
	}
	// Manufacture a conflicting stray entry at Index 2 on replica 2.
	if err := c.replicas[2].Append(replica.Entry{Index: 2, Term: 9, Data: "stray"}); err != nil {
		t.Fatal(err)
	}
	// Heal and propose again: replica 2 must truncate and re-replicate.
	for id := 2; id < 5; id++ {
		c.SetFault(id, FaultNone, 0)
	}
	if _, committed := mustPropose(t, c, 3, "e3"); !committed {
		t.Fatal("e3 must commit after healing")
	}
	if c.Commit() != 3 {
		t.Fatalf("Commit() = %d, want 3", c.Commit())
	}
	if e, _ := c.replicas[2].Get(2); e.Data != "e2" || e.Term != 2 {
		t.Fatalf("replica 2 entry 2 = %+v, want repaired e2", e)
	}
	// Prefix invariant across every pair of committed snapshots.
	snaps := make([][]string, 5)
	for id := 0; id < 5; id++ {
		snaps[id] = c.Snapshot(id)
	}
	for i := 0; i < 5; i++ {
		for j := 0; j < 5; j++ {
			short, long := snaps[i], snaps[j]
			if len(short) > len(long) {
				short, long = long, short
			}
			if !reflect.DeepEqual(long[:len(short)], short) {
				t.Fatalf("snapshots diverge: %v vs %v", snaps[i], snaps[j])
			}
		}
	}
	want := []string{"e1", "e2", "e3"}
	for id := 0; id < 5; id++ {
		if !reflect.DeepEqual(snaps[id], want) {
			t.Fatalf("replica %d snapshot = %v, want %v", id, snaps[id], want)
		}
	}
}

func TestTermRegressionRejected(t *testing.T) {
	c := newCluster(3)
	mustPropose(t, c, 5, "a")
	if _, _, err := c.Propose(4, "b"); err == nil {
		t.Fatal("term regression must be rejected")
	}
	// The rejected proposal must not consume an Index.
	idx, committed := mustPropose(t, c, 5, "b")
	if idx != 2 || !committed {
		t.Fatalf("idx = %d, committed = %v, want 2, true", idx, committed)
	}
}
