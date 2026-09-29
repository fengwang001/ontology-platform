package chainreplication

import (
	"errors"
	"testing"
)

// TestMiddleFailureResendsExactlyGap verifies that after a middle node
// fails, the predecessor resends ONLY seqs greater than the new successor's
// max applied seq: no duplicates below it, nothing above its own applied.
func TestMiddleFailureResendsExactlyGap(t *testing.T) {
	rec := newRecorder()
	c, net := newTestCoordinator(t, []string{"H", "M", "T"}, rec)

	for _, v := range []string{"a", "b", "c", "d", "e"} {
		if _, err := c.Write("H", v); err != nil {
			t.Fatal(err)
		}
	}
	// Propagate 1..5 H->M.
	for i := 0; i < 5; i++ {
		net.Deliver(0)
	}
	// Propagate only 1..3 M->T (T applied=3); 4,5 are held at M.
	for seq := 1; seq <= 3; seq++ {
		if !deliverOne(net, KindWrite, "M", "T", seq) {
			t.Fatalf("missing M->T %d", seq)
		}
	}
	net.DropAll() // discard the acks from T for a clean observation point
	if c.nodes["T"].applied != 3 || c.nodes["M"].applied != 5 {
		t.Fatalf("setup wrong: T=%d M=%d", c.nodes["T"].applied, c.nodes["M"].applied)
	}

	if err := c.Fail("M"); err != nil {
		t.Fatal(err)
	}

	// Exactly the gap writes seq 4 and 5 must be resent H->T.
	pending := net.Pending()
	if len(pending) != 2 {
		t.Fatalf("want exactly 2 resent messages, got %+v", pending)
	}
	got := map[int]bool{}
	for _, m := range pending {
		if m.Kind != KindWrite || m.From != "H" || m.To != "T" {
			t.Fatalf("resent message wrong route: %+v", m)
		}
		got[m.Seq] = true
	}
	if !got[4] || !got[5] {
		t.Fatalf("resent seqs = %v, want {4,5}", got)
	}

	deliverAll(c, net)
	if c.nodes["T"].applied != 5 {
		t.Fatalf("T applied=%d, want 5", c.nodes["T"].applied)
	}
	for seq := 1; seq <= 5; seq++ {
		if v, seen := rec.outcome(seq); !seen || !v {
			t.Fatalf("seq %d must commit after gap refill: seen=%v committed=%v", seq, seen, v)
		}
	}
	entries, _ := c.Read("T")
	if len(entries) != 5 {
		t.Fatalf("committed entries=%v", entries)
	}
}

// TestTailFailureCommitsImmediately: predecessor becomes tail and commits
// its entire pending-confirmation set right away.
func TestTailFailureCommitsImmediately(t *testing.T) {
	rec := newRecorder()
	c, net := newTestCoordinator(t, []string{"H", "M", "T"}, rec)

	for _, v := range []string{"x", "y"} {
		if _, err := c.Write("H", v); err != nil {
			t.Fatal(err)
		}
	}
	// Reach M fully (M applied=2); nothing reaches T.
	for i := 0; i < 2; i++ {
		net.Deliver(0)
	}
	net.DropAll()

	if err := c.Fail("T"); err != nil {
		t.Fatal(err)
	}
	if got := c.Chain(); len(got) != 2 || got[1] != "M" {
		t.Fatalf("chain=%v", got)
	}
	deliverAll(c, net)
	for seq := 1; seq <= 2; seq++ {
		if v, seen := rec.outcome(seq); !seen || !v {
			t.Fatalf("seq %d must commit immediately after tail failure", seq)
		}
	}
	entries, err := c.Read("M")
	if err != nil || len(entries) != 2 {
		t.Fatalf("read new tail: %v err=%v", entries, err)
	}
	if _, err := c.Read("H"); !errors.Is(err, ErrReadNotAtTail) {
		t.Fatalf("read at non-tail must reject, got %v", err)
	}

	// New writes continue through the shortened chain.
	if _, err := c.Write("H", "z"); err != nil {
		t.Fatal(err)
	}
	deliverAll(c, net)
	if v, seen := rec.outcome(3); !seen || !v {
		t.Fatalf("seq3 after repair seen=%v committed=%v", seen, v)
	}
}

// TestTailFailureDownToSingleNode: tail fails in a 2-node chain; the head
// becomes the sole head+tail node and commits immediately.
func TestTailFailureDownToSingleNode(t *testing.T) {
	rec := newRecorder()
	c, net := newTestCoordinator(t, []string{"H", "T"}, rec)
	if _, err := c.Write("H", "only"); err != nil {
		t.Fatal(err)
	}
	if err := c.Fail("T"); err != nil {
		t.Fatal(err)
	}
	if v, seen := rec.outcome(1); !seen || !v {
		t.Fatalf("sole node must commit immediately, seen=%v committed=%v", seen, v)
	}
	entries, _ := c.Read("H")
	if len(entries) != 1 || entries[0].Val != "only" {
		t.Fatalf("entries=%v", entries)
	}
	if _, err := c.Write("H", "solo"); err != nil {
		t.Fatal(err)
	}
	if v, seen := rec.outcome(2); !seen || !v {
		t.Fatalf("seq2 seen=%v committed=%v", seen, v)
	}
	_ = net
}

// TestSingleNodeWritesAndReads covers head==tail synchronous commit.
func TestSingleNodeWritesAndReads(t *testing.T) {
	rec := newRecorder()
	c, _ := newTestCoordinator(t, []string{"S"}, rec)
	for _, v := range []string{"p", "q"} {
		if _, err := c.Write("S", v); err != nil {
			t.Fatal(err)
		}
	}
	if rec.count() != 2 {
		t.Fatalf("solo commits=%d", rec.count())
	}
	entries, err := c.Read("S")
	if err != nil || len(entries) != 2 {
		t.Fatalf("read=%v err=%v", entries, err)
	}
}
