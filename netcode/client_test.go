package netcode

import (
	"reflect"
	"testing"
)

func mustClient(t *testing.T, cfg Config, player string) *Client {
	t.Helper()
	c, err := NewClient(cfg, player)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestClientPredictsImmediately(t *testing.T) {
	cfg := Config{W: 10, K: 1, M: 5, P: 10}
	c := mustClient(t, cfg, "p")

	if _, err := c.Submit(0); err.(*Error).Reason != ReasonInvalidDelta {
		t.Fatalf("Submit(0) err = %v", err)
	}
	if _, err := c.Submit(4); err != nil {
		t.Fatal(err)
	}
	if pos, _ := c.Predicted(); pos != 4 {
		t.Fatalf("predicted = %d, want 4", pos)
	}
	// Over-limit delta predicts as rejected: position unchanged, but the
	// move is still tracked as unacked.
	if _, err := c.Submit(99); err != nil {
		t.Fatal(err)
	}
	pos, unacked := c.Predicted()
	if pos != 4 || !reflect.DeepEqual(unacked, []int64{1, 2}) {
		t.Fatalf("predicted=%d unacked=%v, want 4 [1 2]", pos, unacked)
	}
	// Clamp applies in prediction.
	if _, err := c.Submit(5); err != nil { // 4+5=9
		t.Fatal(err)
	}
	if _, err := c.Submit(5); err != nil { // 9+5=14 -> clamp 10
		t.Fatal(err)
	}
	if pos, _ := c.Predicted(); pos != 10 {
		t.Fatalf("predicted = %d, want 10 (clamped)", pos)
	}
}

func TestClientApplyAckReplaysRemainder(t *testing.T) {
	cfg := Config{W: 100, K: 2, M: 100, P: 10}
	c := mustClient(t, cfg, "p")
	for _, d := range []int64{3, 3, 3, 3} {
		if _, err := c.Submit(d); err != nil {
			t.Fatal(err)
		}
	}
	// Server processed seqs 1-2 landing at 6; client replays 3-4 on top.
	if !c.ApplyAck(Ack{Player: "p", ProcessedSeq: 2, Position: 6}) {
		t.Fatal("ack not applied")
	}
	pos, unacked := c.Predicted()
	if pos != 12 || !reflect.DeepEqual(unacked, []int64{3, 4}) {
		t.Fatalf("predicted=%d unacked=%v, want 12 [3 4]", pos, unacked)
	}
	if got := c.Divergence(); got != 6 {
		t.Fatalf("divergence = %d, want 6", got)
	}
	if got := c.LastAcked(); got != 2 {
		t.Fatalf("lastAcked = %d, want 2", got)
	}
}

func TestClientIgnoresStaleAndDuplicateAcks(t *testing.T) {
	cfg := Config{W: 100, K: 4, M: 100, P: 10}
	c := mustClient(t, cfg, "p")
	for _, d := range []int64{1, 2, 3, 4} {
		if _, err := c.Submit(d); err != nil {
			t.Fatal(err)
		}
	}
	if !c.ApplyAck(Ack{Player: "p", ProcessedSeq: 3, Position: 6}) {
		t.Fatal("fresh ack not applied")
	}
	// Duplicate of the same ack and an older ack are both ignored.
	if c.ApplyAck(Ack{Player: "p", ProcessedSeq: 3, Position: 6}) {
		t.Fatal("duplicate ack applied")
	}
	if c.ApplyAck(Ack{Player: "p", ProcessedSeq: 2, Position: 3}) {
		t.Fatal("stale ack applied")
	}
	// An ack for another player is ignored.
	if c.ApplyAck(Ack{Player: "q", ProcessedSeq: 9, Position: 99}) {
		t.Fatal("foreign ack applied")
	}
	pos, unacked := c.Predicted()
	if pos != 10 || !reflect.DeepEqual(unacked, []int64{4}) {
		t.Fatalf("predicted=%d unacked=%v, want 10 [4]", pos, unacked)
	}
}

func TestClientAckOrderIndependence(t *testing.T) {
	cfg := Config{W: 100, K: 2, M: 100, P: 10}
	acks := []Ack{
		{Player: "p", ProcessedSeq: 2, Position: 5},
		{Player: "p", ProcessedSeq: 4, Position: 9},
		{Player: "p", ProcessedSeq: 6, Position: 13},
	}
	orders := [][]int{
		{0, 1, 2}, {2, 1, 0}, {1, 2, 0}, {2, 0, 1}, {0, 2, 1}, {1, 0, 2},
	}
	var wantPos int64 = -1
	for _, order := range orders {
		c := mustClient(t, cfg, "p")
		for i := 0; i < 8; i++ {
			if _, err := c.Submit(2); err != nil {
				t.Fatal(err)
			}
		}
		for _, i := range order {
			c.ApplyAck(acks[i])
			c.ApplyAck(acks[i]) // duplicates must not matter
		}
		pos, _ := c.Predicted()
		if wantPos == -1 {
			wantPos = pos
		} else if pos != wantPos {
			t.Fatalf("order %v: pos=%d, want %d", order, pos, wantPos)
		}
	}
	// 13 + two remaining moves of 2 = 17.
	if wantPos != 17 {
		t.Fatalf("converged pos = %d, want 17", wantPos)
	}
}

func TestClientDoesNotReplayServerRejected(t *testing.T) {
	cfg := Config{W: 100, K: 2, M: 5, P: 10}
	c := mustClient(t, cfg, "p")
	if _, err := c.Submit(10); err != nil { // seq 1: over limit
		t.Fatal(err)
	}
	if _, err := c.Submit(3); err != nil { // seq 2
		t.Fatal(err)
	}
	// Server rejected seq 1 but it still consumed quota and advanced the
	// processed seq. The client must not replay seq 1 as a success.
	if !c.ApplyAck(Ack{Player: "p", ProcessedSeq: 2, Position: 3, Rejected: []int64{1}}) {
		t.Fatal("ack not applied")
	}
	pos, unacked := c.Predicted()
	if pos != 3 || len(unacked) != 0 {
		t.Fatalf("predicted=%d unacked=%v, want 3 []", pos, unacked)
	}
	if got := c.Divergence(); got != 0 {
		t.Fatalf("divergence = %d, want 0", got)
	}
}

func TestClientDefensiveRejectedBeyondProcessed(t *testing.T) {
	// A misbehaving ack claims a rejection for a seq beyond ProcessedSeq;
	// the client must skip it during replay until it is confirmed.
	cfg := Config{W: 100, K: 2, M: 100, P: 10}
	c := mustClient(t, cfg, "p")
	if _, err := c.Submit(5); err != nil { // seq 1
		t.Fatal(err)
	}
	if _, err := c.Submit(5); err != nil { // seq 2
		t.Fatal(err)
	}
	c.ApplyAck(Ack{Player: "p", ProcessedSeq: 1, Position: 5, Rejected: []int64{2}})
	pos, unacked := c.Predicted()
	if pos != 5 || !reflect.DeepEqual(unacked, []int64{2}) {
		t.Fatalf("predicted=%d unacked=%v, want 5 [2] (seq 2 skipped)", pos, unacked)
	}
	// Once seq 2 is confirmed processed, the defensive record is pruned.
	c.ApplyAck(Ack{Player: "p", ProcessedSeq: 2, Position: 5, Rejected: []int64{2}})
	if pos, unacked := c.Predicted(); pos != 5 || len(unacked) != 0 {
		t.Fatalf("predicted=%d unacked=%v, want 5 []", pos, unacked)
	}
}

func TestClientServerFinalConsistency(t *testing.T) {
	cfg := Config{W: 20, K: 2, M: 4, P: 8}
	s := mustServer(t, cfg, "p")
	c := mustClient(t, cfg, "p")

	deltas := []int64{3, 9, -2, 4, 4, 4, -9, 1, 2, 3}
	var pendingAcks []Ack
	now := int64(0)
	for _, d := range deltas {
		mv, err := c.Submit(d)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Receive("p", mv); err != nil {
			t.Fatal(err)
		}
		now++
		acks, err := s.Tick(now)
		if err != nil {
			t.Fatal(err)
		}
		pendingAcks = append(pendingAcks, acks...)
	}
	// Drain the backlog.
	for {
		n, _ := s.PendingLen("p")
		if n == 0 {
			break
		}
		now++
		acks, err := s.Tick(now)
		if err != nil {
			t.Fatal(err)
		}
		pendingAcks = append(pendingAcks, acks...)
	}
	// Deliver acks in reverse order (worst-case reordering).
	for i := len(pendingAcks) - 1; i >= 0; i-- {
		c.ApplyAck(pendingAcks[i])
	}
	srvPos, srvSeq, _ := s.Authoritative("p")
	clPos, unacked := c.Predicted()
	if clPos != srvPos || len(unacked) != 0 || c.Divergence() != 0 {
		t.Fatalf("client=(%d,%v) server=(%d,seq=%d)", clPos, unacked, srvPos, srvSeq)
	}
	if c.LastAcked() != srvSeq {
		t.Fatalf("lastAcked=%d, want %d", c.LastAcked(), srvSeq)
	}
}
