package cluster

import (
	"reflect"
	"testing"
)

// Semantic 1: once committed, an entry survives any later fault
// injection and any later proposals.
func TestCommitDurability(t *testing.T) {
	c := New(5, nil)
	idx, committed, err := c.Propose(1, "durable")
	if err != nil || !committed || idx != 1 {
		t.Fatalf("Propose = (%d, %v, %v)", idx, committed, err)
	}
	// Inject faults on a majority and keep proposing.
	for id := 0; id < 3; id++ {
		c.SetFault(id, FaultReject, 0)
	}
	if _, _, err := c.Propose(2, "later"); err != nil {
		t.Fatalf("later propose: %v", err)
	}
	for id := 0; id < 5; id++ {
		snap := c.Snapshot(id)
		if len(snap) < 1 || snap[0] != "durable" {
			t.Fatalf("replica %d lost committed entry: %v", id, snap)
		}
	}
	if got := c.Commit(); got != 1 {
		t.Fatalf("Commit = %d, want 1", got)
	}
}

// Semantic 3: without majority acks, no commit and no committed
// snapshot entry anywhere.
func TestNoCommitWithoutMajority(t *testing.T) {
	c := New(5, nil)
	c.SetFault(2, FaultReject, 0)
	c.SetFault(3, FaultDrop, 0)
	c.SetFault(4, FaultDrop, 0)
	idx, committed, err := c.Propose(1, "uncommitted")
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if committed {
		t.Fatal("must not commit with only 2 of 5 acks")
	}
	if got := c.Commit(); got != 0 {
		t.Fatalf("Commit advanced to %d without majority", got)
	}
	for id := 0; id < 5; id++ {
		if snap := c.Snapshot(id); len(snap) != 0 {
			t.Fatalf("replica %d exposes uncommitted data: %v", id, snap)
		}
	}
	_ = idx
}

// Semantic 5: a lagging replica is backfilled by later proposals and
// ends up identical to the other committed replicas.
func TestLagCatchUp(t *testing.T) {
	c := New(3, nil)
	c.SetFault(2, FaultLag, 2)
	var last uint64
	for i := 0; i < 6; i++ {
		idx, committed, err := c.Propose(1, "entry")
		if err != nil || !committed {
			t.Fatalf("Propose %d = (%d, %v, %v)", i, idx, committed, err)
		}
		last = idx
	}
	if last != 6 {
		t.Fatalf("last index = %d, want 6", last)
	}
	// Heal the fault; the next proposal backfills the missing tail.
	c.SetFault(2, FaultNone, 0)
	if _, _, err := c.Propose(1, "entry"); err != nil {
		t.Fatalf("catch-up propose: %v", err)
	}
	base := c.Snapshot(0)
	if got := c.Snapshot(2); !reflect.DeepEqual(got, base) {
		t.Fatalf("lagging replica diverged: %v vs %v", got, base)
	}
}

// Semantic 6: ReadIndex requires a reachable majority.
func TestReadIndex(t *testing.T) {
	c := New(3, nil)
	if _, committed, _ := c.Propose(1, "x"); !committed {
		t.Fatal("expected commit")
	}
	ri, err := c.ReadIndex()
	if err != nil || ri < c.Commit() {
		t.Fatalf("ReadIndex = (%d, %v), commit = %d", ri, err, c.Commit())
	}
	c.SetFault(1, FaultDrop, 0)
	c.SetFault(2, FaultReject, 0)
	if _, err := c.ReadIndex(); err == nil {
		t.Fatal("ReadIndex must fail without a reachable majority")
	}
}

// Semantic 7: committed snapshots never fork — the shorter is always a
// prefix of the longer, even across fault-induced divergence.
func TestPrefixInvariant(t *testing.T) {
	c := New(5, nil)
	c.SetFault(3, FaultLag, 3)
	c.SetFault(4, FaultLag, 1)
	for i := 0; i < 8; i++ {
		if _, _, err := c.Propose(1, "data"); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		snaps := make([][]string, 5)
		for id := 0; id < 5; id++ {
			snaps[id] = c.Snapshot(id)
		}
		for a := 0; a < 5; a++ {
			for b := 0; b < 5; b++ {
				assertPrefix(t, snaps[a], snaps[b])
			}
		}
	}
}

func assertPrefix(t *testing.T, a, b []string) {
	t.Helper()
	short, long := a, b
	if len(short) > len(long) {
		short, long = long, short
	}
	for i := range short {
		if short[i] != long[i] {
			t.Fatalf("fork: %v is not a prefix of %v", a, b)
		}
	}
}

func TestTermRegressionRejected(t *testing.T) {
	c := New(1, nil)
	if _, _, err := c.Propose(5, "high"); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if _, _, err := c.Propose(4, "low"); err == nil {
		t.Fatal("expected term regression error")
	}
	if got := c.Commit(); got != 1 {
		t.Fatalf("Commit = %d, want 1", got)
	}
}
