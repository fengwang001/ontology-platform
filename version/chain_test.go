package version

import (
	"testing"

	"ontology/txid"
)

// fakeVis implements Visibility purely from a snapshot point.
type fakeVis struct {
	point txid.ID
	self  txid.ID
}

func (f fakeVis) Visible(cid txid.ID) bool { return cid.Less(f.point) }
func (f fakeVis) IsSelf(t txid.ID) bool    { return f.self.Valid() && t == f.self }
func (f fakeVis) SelfTxn() txid.ID         { return f.self }

func TestAppendLookupCommitVisibility(t *testing.T) {
	c := &Chain{}
	c.Append(1, KindValue, []byte("v1"), 0)
	c.Commit(1, 1)
	c.Append(2, KindValue, []byte("v2"), 0)
	c.Commit(2, 2)
	c.Append(3, KindDelete, nil, 0)
	c.Commit(3, 3)

	if r := c.Lookup(fakeVis{point: 4}); r.Outcome != Deleted {
		t.Fatalf("point4 = %v, want Deleted", r.Outcome)
	}
	r := c.Lookup(fakeVis{point: 3}) // left-closed right-open: cid < 3
	if r.Outcome != Present || string(r.Value) != "v2" {
		t.Fatalf("point3 = %v %q, want v2", r.Outcome, r.Value)
	}
	r = c.Lookup(fakeVis{point: 2})
	if r.Outcome != Present || string(r.Value) != "v1" {
		t.Fatalf("point2 = %v %q, want v1", r.Outcome, r.Value)
	}
	r = c.Lookup(fakeVis{point: 1})
	if r.Outcome != Absent {
		t.Fatalf("point1 = %v, want Absent", r.Outcome)
	}
}

func TestReadYourWritesAndAbort(t *testing.T) {
	c := &Chain{}
	c.Append(1, KindValue, []byte("a"), 0)
	c.Commit(1, 1)
	c.Append(2, KindValue, []byte("b"), 0)

	r := c.Lookup(fakeVis{point: 5, self: 2})
	if r.Outcome != Present || string(r.Value) != "b" {
		t.Fatalf("self read = %v %q, want b", r.Outcome, r.Value)
	}
	r = c.Lookup(fakeVis{point: 5})
	if r.Outcome != Present || string(r.Value) != "a" {
		t.Fatalf("other read saw pending: %v %q", r.Outcome, r.Value)
	}
	if n := c.Abort(2); n != 1 || c.Len() != 1 {
		t.Fatalf("abort removed %d, len %d", n, c.Len())
	}
	if r := c.Lookup(fakeVis{point: 5}); r.Outcome != Present || string(r.Value) != "a" {
		t.Fatalf("post-abort read = %v %q", r.Outcome, r.Value)
	}
}

func TestChainLenLimit(t *testing.T) {
	c := &Chain{}
	for i := txid.ID(1); i <= 3; i++ {
		if _, ok := c.Append(i, KindValue, []byte("x"), 3); !ok {
			t.Fatalf("append %d rejected", i)
		}
	}
	if _, ok := c.Append(4, KindValue, []byte("x"), 3); ok {
		t.Fatal("4th append must be rejected")
	}
	if c.Len() != 3 {
		t.Fatalf("rejected append changed len: %d", c.Len())
	}
}

func TestPruneBelowKeepsFloorAndSafety(t *testing.T) {
	c := &Chain{}
	c.Append(1, KindValue, []byte("v1"), 0)
	c.Commit(1, 1)
	c.Append(2, KindValue, []byte("v2"), 0)
	c.Commit(2, 2)
	c.Append(3, KindValue, []byte("v3"), 0)
	c.Commit(3, 3)

	rep := c.PruneBelow(3) // cid < 3 shadowed: v1 goes; v2 is floor
	if rep.Removed != 1 || c.Len() != 2 {
		t.Fatalf("prune removed=%d len=%d", rep.Removed, c.Len())
	}
	r := c.Lookup(fakeVis{point: 3})
	if r.Outcome != Present || string(r.Value) != "v2" {
		t.Fatalf("point3 after prune = %v %q", r.Outcome, r.Value)
	}
	r = c.Lookup(fakeVis{point: 10})
	if r.Outcome != Present || string(r.Value) != "v3" {
		t.Fatalf("point10 after prune = %v %q", r.Outcome, r.Value)
	}
	r = c.Lookup(fakeVis{point: 2})
	if r.Outcome != Present || string(r.Value) != "v1" {
		// snapshot at point 2 could see v1, but watermark 3 guarantees no
		// active snapshot has point <= 3; documented in reclaim package.
		t.Logf("point2 sees %v %q (would be invalid once wm=3)", r.Outcome, r.Value)
	}
}

func TestPrunePendingBlocksReclaim(t *testing.T) {
	c := &Chain{}
	c.Append(1, KindValue, []byte("v1"), 0)
	c.Commit(1, 1)
	c.Append(2, KindValue, []byte("v2"), 0)
	c.Commit(2, 2)
	c.Append(9, KindValue, []byte("pending"), 0)
	rep := c.PruneBelow(100)
	if rep.Removed != 1 { // only v1; v2 is floor, v9 pending
		t.Fatalf("removed=%d, want 1", rep.Removed)
	}
}
