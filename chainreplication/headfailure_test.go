package chainreplication

import "testing"

// TestHeadFailureUncommitted: writes the new head has not applied are
// reported uncommitted; applied writes still commit via the tail.
func TestHeadFailureUncommitted(t *testing.T) {
	rec := newRecorder()
	c, net := newTestCoordinator(t, []string{"H", "M", "T"}, rec)

	// seq1 fully commits.
	if _, err := c.Write("H", "one"); err != nil {
		t.Fatal(err)
	}
	net.Deliver(0) // H->M 1
	net.Deliver(0) // M->T 1
	deliverAll(c, net)

	// seq2 reaches M only; seq3,4 stay queued H->M.
	for _, v := range []string{"two", "three", "four"} {
		if _, err := c.Write("H", v); err != nil {
			t.Fatal(err)
		}
	}
	if !deliverOne(net, KindWrite, "H", "M", 2) {
		t.Fatal("missing H->M 2")
	}

	if err := c.Fail("H"); err != nil {
		t.Fatal(err)
	}
	if got := c.Chain(); len(got) != 2 || got[0] != "M" || got[1] != "T" {
		t.Fatalf("chain=%v", got)
	}

	for _, seq := range []int{3, 4} {
		if v, seen := rec.outcome(seq); !seen || v {
			t.Fatalf("seq %d must be uncommitted, seen=%v committed=%v", seq, seen, v)
		}
		if c.IsCommitted(seq) {
			t.Fatalf("seq %d must not be committed", seq)
		}
	}

	deliverAll(c, net)
	if v, seen := rec.outcome(2); !seen || !v {
		t.Fatalf("seq2 seen=%v committed=%v (must survive via tail)", seen, v)
	}
	if v, seen := rec.outcome(1); !seen || !v {
		t.Fatalf("seq1 seen=%v committed=%v", seen, v)
	}

	// Fresh writes on the new head keep the global sequence (start at 5).
	if seq, err := c.Write("M", "five"); err != nil || seq != 5 {
		t.Fatalf("write at new head seq=%d err=%v", seq, err)
	}
	deliverAll(c, net)
	if v, seen := rec.outcome(5); !seen || !v {
		t.Fatalf("seq5 seen=%v committed=%v", seen, v)
	}
}

// TestHeadFailureAckRecovered: tail committed past the new head's acked
// watermark and those acks died with the old head; the new head must still
// learn the commits (an acknowledged write is never lost).
func TestHeadFailureAckRecovered(t *testing.T) {
	rec := newRecorder()
	c, net := newTestCoordinator(t, []string{"H", "M", "T"}, rec)
	for _, v := range []string{"a", "b", "c", "d"} {
		if _, err := c.Write("H", v); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 4; i++ {
		if !deliverOne(net, KindWrite, "H", "M", i) {
			t.Fatal("missing H->M")
		}
		if !deliverOne(net, KindWrite, "M", "T", i) {
			t.Fatal("missing M->T")
		}
	}
	net.DropAll() // acks in flight are discarded together with the old head

	if err := c.Fail("H"); err != nil {
		t.Fatal(err)
	}
	deliverAll(c, net)
	for seq := 1; seq <= 4; seq++ {
		if v, seen := rec.outcome(seq); !seen || !v {
			t.Fatalf("seq %d already at tail must commit to new head, seen=%v committed=%v", seq, seen, v)
		}
	}
	entries, _ := c.Read("T")
	if len(entries) != 4 {
		t.Fatalf("tail entries=%v", entries)
	}
}

// TestHeadFailureBufferedHoleDropped: seq2 reaches the new head out of
// order while seq1 is stuck at the dead head; the orphan is dropped and
// seq2 reported uncommitted without blocking later writes.
func TestHeadFailureBufferedHoleDropped(t *testing.T) {
	rec := newRecorder()
	c, net := newTestCoordinator(t, []string{"H", "M", "T"}, rec)
	if _, err := c.Write("H", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write("H", "b"); err != nil {
		t.Fatal(err)
	}
	if !deliverOne(net, KindWrite, "H", "M", 2) {
		t.Fatal("missing H->M 2")
	}
	if c.nodes["M"].applied != 0 {
		t.Fatal("M must not apply past a hole")
	}
	if err := c.Fail("H"); err != nil {
		t.Fatal(err)
	}
	for seq := 1; seq <= 2; seq++ {
		if v, seen := rec.outcome(seq); !seen || v {
			t.Fatalf("seq %d must be uncommitted, seen=%v committed=%v", seq, seen, v)
		}
	}
	if _, err := c.Write("M", "c"); err != nil {
		t.Fatal(err)
	}
	deliverAll(c, net)
	if c.nodes["M"].applied != 3 {
		t.Fatalf("new head applied=%d, want 3 (watermark jumps lost seqs 1,2)", c.nodes["M"].applied)
	}
	if v, seen := rec.outcome(3); !seen || !v {
		t.Fatalf("seq3 seen=%v committed=%v", seen, v)
	}
	entries, _ := c.Read("T")
	if len(entries) != 1 || entries[0] != (Entry{3, "c"}) {
		t.Fatalf("tail must expose only committed seq3, got %+v", entries)
	}
}

// TestReadNeverReturnsUncommitted: a write buffered behind a hole is never
// readable, including across a middle-node repair.
func TestReadNeverReturnsUncommitted(t *testing.T) {
	c, net := newTestCoordinator(t, []string{"H", "M", "T"}, nil)

	if _, err := c.Write("H", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write("H", "b"); err != nil {
		t.Fatal(err)
	}
	net.Deliver(0) // H->M 1
	net.Deliver(0) // H->M 2
	if !deliverOne(net, KindWrite, "M", "T", 2) {
		t.Fatal("missing M->T 2")
	}
	if entries, _ := c.Read("T"); len(entries) != 0 {
		t.Fatalf("tail must hide buffered hole, got %v", entries)
	}
	if err := c.Fail("M"); err != nil {
		t.Fatal(err)
	}
	if entries, _ := c.Read("T"); len(entries) != 0 {
		t.Fatalf("tail must stay empty before gap fill, got %v", entries)
	}
	deliverAll(c, net)
	entries, _ := c.Read("T")
	if len(entries) != 2 || entries[0].Seq != 1 || entries[1].Seq != 2 {
		t.Fatalf("entries=%v", entries)
	}
}
