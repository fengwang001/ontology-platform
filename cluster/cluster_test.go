package cluster

import (
	"reflect"
	"testing"
	"time"

	"ontology/replica"
)

func testClock() func() time.Time {
	return func() time.Time { return time.Unix(1700000000, 0) }
}

// Semantics 1: once committed, an entry survives any later fault
// injection and any later proposal; Commit never regresses.
func TestCommittedEntryNeverLost(t *testing.T) {
	c := New(3, testClock())
	idx, ok, err := c.Propose(1, "durable")
	if err != nil || !ok || idx != 1 {
		t.Fatalf("propose: idx=%d ok=%v err=%v", idx, ok, err)
	}
	// Inject faults everywhere and keep proposing.
	c.SetFault(1, FaultDrop, 0)
	c.SetFault(2, FaultReject, 0)
	if _, ok, _ := c.Propose(2, "uncommitted"); ok {
		t.Fatal("second propose must not commit under faults")
	}
	if got := c.Commit(); got != 1 {
		t.Fatalf("Commit regressed or jumped: %d", got)
	}
	// The committed entry is still on a majority (replica 0 here, plus
	// it remains first in every committed snapshot).
	if s := c.Snapshot(0); !reflect.DeepEqual(s, []string{"durable"}) {
		t.Fatalf("snapshot = %v", s)
	}
	// Heal and confirm the entry is still intact cluster-wide.
	c.SetFault(1, FaultNone, 0)
	c.SetFault(2, FaultNone, 0)
	if _, ok, _ := c.Propose(3, "after-heal"); !ok {
		t.Fatal("propose after heal must commit")
	}
	for id := 0; id < 3; id++ {
		s := c.Snapshot(id)
		if len(s) != 3 || s[0] != "durable" {
			t.Fatalf("replica %d snapshot = %v", id, s)
		}
	}
}

// Semantics 3: without a majority of acks, Propose reports
// committed=false, Commit does not move, and no snapshot shows the entry.
func TestNoCommitWithoutMajority(t *testing.T) {
	c := New(5, testClock())
	c.SetFault(3, FaultReject, 0)
	c.SetFault(4, FaultDrop, 0)
	c.SetFault(2, FaultDrop, 0)
	idx, ok, err := c.Propose(1, "stuck")
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if ok {
		t.Fatal("must not commit with only 2 of 5 reachable")
	}
	if got := c.Commit(); got != 0 {
		t.Fatalf("Commit = %d, want 0", got)
	}
	for id := 0; id < 5; id++ {
		if s := c.Snapshot(id); len(s) != 0 {
			t.Fatalf("replica %d snapshot = %v, want empty", id, s)
		}
	}
	// The entry may sit on some replicas, uncommitted. Heal: the next
	// propose re-replicates it and both entries commit.
	c.SetFault(2, FaultNone, 0)
	c.SetFault(3, FaultNone, 0)
	c.SetFault(4, FaultNone, 0)
	if _, ok, _ := c.Propose(1, "second"); !ok {
		t.Fatal("propose after heal must commit")
	}
	if got := c.Commit(); got != idx+1 {
		t.Fatalf("Commit = %d, want %d", got, idx+1)
	}
}

// Semantics 5: a lagging replica is caught up by later proposals and
// ends up byte-for-byte identical to the other committed replicas.
func TestLaggingReplicaCatchesUp(t *testing.T) {
	c := New(3, testClock())
	c.SetFault(2, FaultLag, 2)
	for i := 0; i < 5; i++ {
		if _, ok, _ := c.Propose(1, "e"); !ok {
			t.Fatalf("propose %d must commit with 2/3 acks", i)
		}
	}
	c.SetFault(2, FaultNone, 0)
	if _, ok, _ := c.Propose(1, "final"); !ok {
		t.Fatal("final propose must commit")
	}
	want := c.Snapshot(0)
	if len(want) != 6 {
		t.Fatalf("snapshot len = %d, want 6", len(want))
	}
	for id := 1; id < 3; id++ {
		if s := c.Snapshot(id); !reflect.DeepEqual(s, want) {
			t.Fatalf("replica %d snapshot = %v, want %v", id, s, want)
		}
	}
}

// Semantics 6: ReadIndex requires a reachable majority.
func TestReadIndex(t *testing.T) {
	c := New(3, testClock())
	if _, ok, _ := c.Propose(1, "x"); !ok {
		t.Fatal("propose must commit")
	}
	ri, err := c.ReadIndex()
	if err != nil {
		t.Fatalf("ReadIndex: %v", err)
	}
	if ri < c.Commit() {
		t.Fatalf("ReadIndex = %d < Commit = %d", ri, c.Commit())
	}
	c.SetFault(1, FaultDrop, 0)
	c.SetFault(2, FaultReject, 0)
	if _, err := c.ReadIndex(); err == nil {
		t.Fatal("ReadIndex must fail without a reachable majority")
	}
}

// Semantics 7: conflict -> Truncate -> re-replication keeps every pair
// of committed snapshots in a prefix relationship (no forks).
func TestNoForkAfterConflictAndTruncate(t *testing.T) {
	// Manufacture a fork directly on two local logs.
	a := replica.New(0)
	b := replica.New(1)
	for i := uint64(1); i <= 4; i++ {
		mustAppend(t, a, replica.Entry{Index: i, Term: 1, Data: "a"})
	}
	for i := uint64(1); i <= 2; i++ {
		mustAppend(t, b, replica.Entry{Index: i, Term: 1, Data: "a"})
	}
	// b diverges at index 3 with a different term and payload.
	mustAppend(t, b, replica.Entry{Index: 3, Term: 2, Data: "fork"})
	// Roll back the conflicting tail and re-replicate from a.
	b.Truncate(3)
	for i := uint64(3); i <= 4; i++ {
		e, _ := a.Get(i)
		mustAppend(t, b, e)
	}
	for i := uint64(1); i <= 4; i++ {
		ea, _ := a.Get(i)
		eb, _ := b.Get(i)
		if ea != eb {
			t.Fatalf("index %d diverges: %v vs %v", i, ea, eb)
		}
	}

	// Cluster-level: committed snapshots of any two replicas are always
	// in a prefix relationship, even across fault injection.
	c := New(5, testClock())
	for i := 0; i < 3; i++ {
		c.Propose(1, "p")
	}
	c.SetFault(3, FaultLag, 1)
	c.SetFault(4, FaultDrop, 0)
	c.Propose(1, "q")
	c.SetFault(4, FaultNone, 0)
	c.Propose(1, "r")
	assertSnapshotsPrefixFree(t, c)
}

func assertSnapshotsPrefixFree(t *testing.T, c *Cluster) {
	t.Helper()
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
			for k := range short {
				if short[k] != long[k] {
					t.Fatalf("snapshots %d and %d fork at %d: %v vs %v",
						i, j, k, snaps[i], snaps[j])
				}
			}
		}
	}
}

func mustAppend(t *testing.T, r *replica.Replica, e replica.Entry) {
	t.Helper()
	if err := r.Append(e); err != nil {
		t.Fatalf("append %+v: %v", e, err)
	}
}
