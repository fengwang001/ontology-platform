package chainreplication

import (
	"errors"
	"io"
	"testing"
)

func newTestCoordinator(t *testing.T, chain []string, rec *recorder) (*Coordinator, *QueueNetwork) {
	t.Helper()
	net := NewQueueNetwork(nil)
	var opts []Option
	opts = append(opts, WithLogWriter(io.Discard))
	if rec != nil {
		opts = append(opts, WithResultCallback(rec.callback))
	}
	c, err := NewCoordinator(chain, net, opts...)
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}
	net.Bind(c.Deliver)
	return c, net
}

// deliverAll repeatedly delivers queued messages until no new ones appear.
func deliverAll(c *Coordinator, net *QueueNetwork) {
	for {
		pending := net.Pending()
		if len(pending) == 0 {
			return
		}
		net.Deliver(0)
	}
}

func findMsg(net *QueueNetwork, kind MessageKind, from, to string, seq int) int {
	for i, m := range net.Pending() {
		if m.Kind == kind && m.Seq == seq && m.From == from && m.To == to {
			return i
		}
	}
	return -1
}

func deliverOne(net *QueueNetwork, kind MessageKind, from, to string, seq int) bool {
	idx := findMsg(net, kind, from, to, seq)
	if idx < 0 {
		return false
	}
	net.Deliver(idx)
	return true
}

func TestConstructionRejections(t *testing.T) {
	net := NewQueueNetwork(nil)
	cases := []struct {
		name  string
		chain []string
		want  error
	}{
		{"empty chain", nil, ErrEmptyChain},
		{"empty id", []string{"a", ""}, ErrEmptyNodeID},
		{"duplicate id", []string{"a", "a"}, ErrDuplicateNodeID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewCoordinator(tc.chain, net, WithLogWriter(io.Discard))
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestOperationRejectionsDoNotChangeState(t *testing.T) {
	rec := newRecorder()
	c, net := newTestCoordinator(t, []string{"H", "M", "T"}, rec)

	if _, err := c.Write("M", "x"); !errors.Is(err, ErrWriteNotAtHead) {
		t.Fatalf("write at middle: %v", err)
	}
	if _, err := c.Write("ghost", "x"); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("write unknown: %v", err)
	}
	if _, err := c.Read("M"); !errors.Is(err, ErrReadNotAtTail) {
		t.Fatalf("read at middle: %v", err)
	}
	if _, err := c.Read("ghost"); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("read unknown: %v", err)
	}
	if err := c.Fail("ghost"); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("fail unknown: %v", err)
	}

	// State unchanged: first write still gets seq 1, chain intact.
	if seq, err := c.Write("H", "first"); err != nil || seq != 1 {
		t.Fatalf("write after rejections: seq=%d err=%v", seq, err)
	}
	if got := c.Chain(); len(got) != 3 || got[0] != "H" || got[2] != "T" {
		t.Fatalf("chain changed by rejected ops: %v", got)
	}

	// Failing an already-failed node, and the last alive node, are rejected.
	if err := c.Fail("M"); err != nil {
		t.Fatalf("fail M: %v", err)
	}
	if _, err := c.Write("M", "x"); !errors.Is(err, ErrNodeFailed) {
		t.Fatalf("write at failed node: %v", err)
	}
	if err := c.Fail("M"); !errors.Is(err, ErrNodeFailed) {
		t.Fatalf("re-fail: %v", err)
	}
	if err := c.Fail("T"); err != nil {
		t.Fatalf("fail T: %v", err)
	}
	if err := c.Fail("H"); !errors.Is(err, ErrLastNodeAlive) {
		t.Fatalf("fail last node: %v", err)
	}
	if got := c.Chain(); len(got) != 1 || got[0] != "H" {
		t.Fatalf("last-alive rejection must keep chain, got %v", got)
	}
	_ = net
}

func TestBasicFlowCommitAndRead(t *testing.T) {
	rec := newRecorder()
	c, net := newTestCoordinator(t, []string{"H", "M", "T"}, rec)

	if _, err := c.Write("H", "w1"); err != nil {
		t.Fatal(err)
	}
	// Before propagation: tail read sees nothing uncommitted.
	if entries, err := c.Read("T"); err != nil || len(entries) != 0 {
		t.Fatalf("tail must hide uncommitted, got %v err=%v", entries, err)
	}
	if c.IsCommitted(1) {
		t.Fatal("seq1 must not be committed before ack reaches head")
	}

	deliverAll(c, net)

	if !c.IsCommitted(1) {
		t.Fatal("seq1 must be committed after ack round trip")
	}
	entries, err := c.Read("T")
	if err != nil || len(entries) != 1 || entries[0] != (Entry{1, "w1"}) {
		t.Fatalf("read = %v, err=%v", entries, err)
	}
	if v, seen := rec.outcome(1); !seen || !v {
		t.Fatalf("outcome for 1 = seen=%v committed=%v", seen, v)
	}
}

func TestOutOfOrderBufferingAndDuplicates(t *testing.T) {
	rec := newRecorder()
	c, net := newTestCoordinator(t, []string{"H", "M", "T"}, rec)

	for _, v := range []string{"a", "b", "c"} {
		if _, err := c.Write("H", v); err != nil {
			t.Fatal(err)
		}
	}
	// At M: deliver seq3 and seq2 first (reorder) -> buffered, not applied.
	if !deliverOne(net, KindWrite, "H", "M", 3) {
		t.Fatal("missing H->M 3")
	}
	if !deliverOne(net, KindWrite, "H", "M", 2) {
		t.Fatal("missing H->M 2")
	}
	if c.nodes["M"].applied != 0 {
		t.Fatalf("hole must block application, applied=%d", c.nodes["M"].applied)
	}
	// A second copy of buffered seq3 (duplicate, e.g. network dup) is discarded.
	net.Send(Message{Kind: KindWrite, From: "H", To: "M", Seq: 3, Val: "c"})
	if !deliverOne(net, KindWrite, "H", "M", 3) {
		t.Fatal("missing dup H->M 3")
	}
	if len(net.Pending()) != 1 || net.Pending()[0].Seq != 1 {
		t.Fatalf("only seq1 should remain, got %+v", net.Pending())
	}
	// Deliver seq1: 1,2,3 apply and forward in order.
	net.Deliver(0)
	if c.nodes["M"].applied != 3 {
		t.Fatalf("M applied = %d, want 3", c.nodes["M"].applied)
	}
	// Re-deliver a forwarded write (duplicate at T): discarded, no crash.
	// First reorder again: deliver M->T 3 before 1.
	if !deliverOne(net, KindWrite, "M", "T", 3) {
		t.Fatal("missing M->T 3")
	}
	if c.nodes["T"].applied != 0 {
		t.Fatal("T must buffer seq3 behind a hole")
	}
	deliverAll(c, net)

	if c.nodes["T"].applied != 3 {
		t.Fatalf("T applied = %d, want 3", c.nodes["T"].applied)
	}
	if rec.count() != 3 {
		t.Fatalf("want 3 outcomes, got %d: %+v", rec.count(), rec.commits)
	}
	for seq := 1; seq <= 3; seq++ {
		if v, seen := rec.outcome(seq); !seen || !v {
			t.Fatalf("seq %d outcome seen=%v committed=%v", seq, seen, v)
		}
	}
	// Prefix invariant: applied(T) <= applied(M) <= applied(H).
	if !(c.nodes["T"].applied <= c.nodes["M"].applied && c.nodes["M"].applied <= c.nodes["H"].applied) {
		t.Fatal("prefix invariant violated")
	}
}
