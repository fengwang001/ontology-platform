package netcode

import (
	"errors"
	"reflect"
	"testing"
)

func mustServer(t *testing.T, cfg Config, players ...string) *Server {
	t.Helper()
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	for _, p := range players {
		if err := s.Register(p); err != nil {
			t.Fatalf("Register(%q): %v", p, err)
		}
	}
	return s
}

func mustReceive(t *testing.T, s *Server, player string, seq, delta int64) Receipt {
	t.Helper()
	r, err := s.Receive(player, Move{Seq: seq, Delta: delta})
	if err != nil {
		t.Fatalf("Receive(%q, seq=%d, delta=%d): %v", player, seq, delta, err)
	}
	return r
}

func mustTick(t *testing.T, s *Server, now int64) []Ack {
	t.Helper()
	acks, err := s.Tick(now)
	if err != nil {
		t.Fatalf("Tick(%d): %v", now, err)
	}
	return acks
}

func TestConfigValidate(t *testing.T) {
	valid := Config{W: 100, K: 1, M: 1, P: 1}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	bad := []Config{
		{W: 0, K: 1, M: 1, P: 1},
		{W: MaxW + 1, K: 1, M: 1, P: 1},
		{W: 1, K: 0, M: 1, P: 1},
		{W: 1, K: 51, M: 1, P: 1},
		{W: 1, K: 1, M: 0, P: 1},
		{W: 1, K: 1, M: 1, P: 0},
	}
	for i, cfg := range bad {
		if err := cfg.Validate(); err == nil {
			t.Fatalf("bad config %d accepted: %+v", i, cfg)
		}
	}
}

func TestQuotaExactAndOverflow(t *testing.T) {
	// K=2: four inputs need exactly two ticks; nothing is dropped or
	// reordered, the overflow waits for the next tick.
	s := mustServer(t, Config{W: 100, K: 2, M: 100, P: 10}, "p")
	for seq := int64(1); seq <= 4; seq++ {
		if r := mustReceive(t, s, "p", seq, 3); r.Status != StatusAccepted {
			t.Fatalf("seq %d: got %v", seq, r.Status)
		}
	}
	acks := mustTick(t, s, 1)
	want := []Ack{{Player: "p", ProcessedSeq: 2, Position: 6}}
	if !reflect.DeepEqual(acks, want) {
		t.Fatalf("tick 1 acks = %+v, want %+v", acks, want)
	}
	if n, _ := s.PendingLen("p"); n != 2 {
		t.Fatalf("pending after tick 1 = %d, want 2", n)
	}
	acks = mustTick(t, s, 2)
	want = []Ack{{Player: "p", ProcessedSeq: 4, Position: 12}}
	if !reflect.DeepEqual(acks, want) {
		t.Fatalf("tick 2 acks = %+v, want %+v", acks, want)
	}
	if acks := mustTick(t, s, 3); len(acks) != 0 {
		t.Fatalf("tick 3 with empty backlog produced acks: %+v", acks)
	}
}

func TestRejectedInputConsumesQuota(t *testing.T) {
	// K=1, M=5: seq 1 (delta 10) is rejected but still consumes the whole
	// quota and advances the processed seq; seq 2 waits for the next tick.
	s := mustServer(t, Config{W: 100, K: 1, M: 5, P: 10}, "p")
	mustReceive(t, s, "p", 1, 10)
	mustReceive(t, s, "p", 2, 3)

	acks := mustTick(t, s, 1)
	want := []Ack{{Player: "p", ProcessedSeq: 1, Position: 0, Rejected: []int64{1}}}
	if !reflect.DeepEqual(acks, want) {
		t.Fatalf("tick 1 acks = %+v, want %+v", acks, want)
	}
	acks = mustTick(t, s, 2)
	want = []Ack{{Player: "p", ProcessedSeq: 2, Position: 3}}
	if !reflect.DeepEqual(acks, want) {
		t.Fatalf("tick 2 acks = %+v, want %+v", acks, want)
	}
}

func TestClampCountsAsAccepted(t *testing.T) {
	// delta 50 from 0 clamps to W=10; the clamped position differs from
	// the unclamped one, yet the input is accepted (not in Rejected).
	s := mustServer(t, Config{W: 10, K: 5, M: 100, P: 10}, "p")
	mustReceive(t, s, "p", 1, 50)
	mustReceive(t, s, "p", 2, -30) // clamps 10 -> 0
	acks := mustTick(t, s, 1)
	want := []Ack{{Player: "p", ProcessedSeq: 2, Position: 0}}
	if !reflect.DeepEqual(acks, want) {
		t.Fatalf("acks = %+v, want %+v", acks, want)
	}
}

func TestDuplicateIdempotent(t *testing.T) {
	s := mustServer(t, Config{W: 10, K: 5, M: 10, P: 10}, "p")
	if r := mustReceive(t, s, "p", 1, 4); r.Status != StatusAccepted {
		t.Fatalf("first receive = %v", r.Status)
	}
	for i := 0; i < 3; i++ {
		r := mustReceive(t, s, "p", 1, 4)
		if r.Status != StatusDuplicate || r.Previous != StatusAccepted {
			t.Fatalf("dup receive %d = %+v, want {Duplicate, Previous: Accepted}", i, r)
		}
	}
	if n, _ := s.PendingLen("p"); n != 1 {
		t.Fatalf("pending = %d, want 1 (duplicate must not enqueue)", n)
	}
	// Duplicates of an already-processed seq stay idempotent too.
	mustTick(t, s, 1)
	r := mustReceive(t, s, "p", 1, 4)
	if r.Status != StatusDuplicate || r.Previous != StatusAccepted {
		t.Fatalf("post-tick dup = %+v", r)
	}
}

func TestGapRejectedWithoutStateChange(t *testing.T) {
	s := mustServer(t, Config{W: 10, K: 5, M: 10, P: 10}, "p")
	r := mustReceive(t, s, "p", 3, 4)
	if r.Status != StatusGap {
		t.Fatalf("gap receive = %v, want Gap", r.Status)
	}
	if n, _ := s.PendingLen("p"); n != 0 {
		t.Fatalf("pending = %d after gap, want 0", n)
	}
	// The gap did not advance maxReceived: seq 1 and 2 are still accepted.
	if r := mustReceive(t, s, "p", 1, 4); r.Status != StatusAccepted {
		t.Fatalf("seq 1 after gap = %v", r.Status)
	}
	if r := mustReceive(t, s, "p", 2, 4); r.Status != StatusAccepted {
		t.Fatalf("seq 2 after gap = %v", r.Status)
	}
	// And seq 3 is now a duplicate-free accept.
	if r := mustReceive(t, s, "p", 3, 4); r.Status != StatusAccepted {
		t.Fatalf("seq 3 after filling gap = %v", r.Status)
	}
}

func TestBacklogFull(t *testing.T) {
	s := mustServer(t, Config{W: 100, K: 1, M: 100, P: 2}, "p")
	mustReceive(t, s, "p", 1, 1)
	mustReceive(t, s, "p", 2, 1)
	r := mustReceive(t, s, "p", 3, 1)
	if r.Status != StatusBacklogFull {
		t.Fatalf("overflow receive = %v, want BacklogFull", r.Status)
	}
	// A full-backlog rejection does not advance maxReceived: after one
	// tick frees a slot, seq 3 (not 4) is the next expected seq.
	mustTick(t, s, 1)
	if r := mustReceive(t, s, "p", 3, 1); r.Status != StatusAccepted {
		t.Fatalf("seq 3 after drain = %v", r.Status)
	}
}

func TestClockRollback(t *testing.T) {
	s := mustServer(t, Config{W: 100, K: 1, M: 100, P: 10}, "p")
	mustReceive(t, s, "p", 1, 5)
	mustReceive(t, s, "p", 2, 5)
	mustTick(t, s, 100)

	for _, now := range []int64{100, 50, 0, -1} {
		_, err := s.Tick(now)
		var ne *Error
		if !errors.As(err, &ne) || ne.Reason != ReasonClockRollback {
			t.Fatalf("Tick(%d) err = %v, want clock rollback", now, err)
		}
	}
	// The failed ticks processed nothing: seq 2 is still pending.
	if n, _ := s.PendingLen("p"); n != 1 {
		t.Fatalf("pending = %d after rolled-back ticks, want 1", n)
	}
	acks := mustTick(t, s, 101)
	want := []Ack{{Player: "p", ProcessedSeq: 2, Position: 10}}
	if !reflect.DeepEqual(acks, want) {
		t.Fatalf("acks = %+v, want %+v", acks, want)
	}
}

func TestRejectionPrecedence(t *testing.T) {
	s := mustServer(t, Config{W: 100, K: 1, M: 100, P: 2}, "p")
	mustReceive(t, s, "p", 1, 1)
	mustReceive(t, s, "p", 2, 1) // backlog now full (P=2), maxReceived=2

	// Invalid delta beats everything, even for unknown players.
	if _, err := s.Receive("ghost", Move{Seq: 0, Delta: 0}); err.(*Error).Reason != ReasonInvalidDelta {
		t.Fatalf("delta=0 seq=0 unknown: got %v", err)
	}
	// Invalid seq beats unknown player.
	if _, err := s.Receive("ghost", Move{Seq: 0, Delta: 1}); err.(*Error).Reason != ReasonInvalidSeq {
		t.Fatalf("seq=0 unknown: got %v", err)
	}
	// Unknown player beats domain outcomes.
	if _, err := s.Receive("ghost", Move{Seq: 1, Delta: 1}); err.(*Error).Reason != ReasonUnknownPlayer {
		t.Fatalf("unknown player: got %v", err)
	}
	// Duplicate beats gap and backlog full.
	if r := mustReceive(t, s, "p", 1, 99); r.Status != StatusDuplicate {
		t.Fatalf("dup on full backlog = %v, want Duplicate", r.Status)
	}
	// Gap beats backlog full.
	if r := mustReceive(t, s, "p", 9, 1); r.Status != StatusGap {
		t.Fatalf("gap on full backlog = %v, want Gap", r.Status)
	}
	// Only then backlog full.
	if r := mustReceive(t, s, "p", 3, 1); r.Status != StatusBacklogFull {
		t.Fatalf("next seq on full backlog = %v, want BacklogFull", r.Status)
	}
}

func TestTickSkipsIdlePlayers(t *testing.T) {
	s := mustServer(t, Config{W: 100, K: 1, M: 100, P: 10}, "a", "b", "c")
	mustReceive(t, s, "b", 1, 7)
	acks := mustTick(t, s, 1)
	if len(acks) != 1 || acks[0].Player != "b" {
		t.Fatalf("acks = %+v, want only player b", acks)
	}
}

func TestMultiPlayerIsolation(t *testing.T) {
	s := mustServer(t, Config{W: 100, K: 2, M: 100, P: 10}, "a", "b")
	mustReceive(t, s, "a", 1, 10)
	mustReceive(t, s, "b", 1, 20)
	mustReceive(t, s, "b", 2, 20)
	acks := mustTick(t, s, 1)
	if len(acks) != 2 {
		t.Fatalf("acks = %+v, want 2", acks)
	}
	posA, seqA, _ := s.Authoritative("a")
	posB, seqB, _ := s.Authoritative("b")
	if posA != 10 || seqA != 1 || posB != 40 || seqB != 2 {
		t.Fatalf("a=(%d,%d) b=(%d,%d)", posA, seqA, posB, seqB)
	}
	if _, _, err := s.Authoritative("ghost"); err.(*Error).Reason != ReasonUnknownPlayer {
		t.Fatalf("Authoritative(ghost) = %v", err)
	}
}
