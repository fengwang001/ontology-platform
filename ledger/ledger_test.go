package ledger

import (
	"errors"
	"testing"

	"ontology/shard"
	"ontology/wal"
)

type memSink struct {
	fail bool
}

func (m *memSink) Write(p []byte) (int, error) {
	if m.fail {
		return 0, errors.New("injected disk failure")
	}
	return len(p), nil
}

func newLedger(n int, initial int64) (*Ledger, *wal.Log, *shard.Set, *memSink) {
	sink := &memSink{}
	l := wal.New(sink)
	s := shard.New(n, initial)
	return New(l, s), l, s, sink
}

func TestTransferHappyPath(t *testing.T) {
	g, l, s, _ := newLedger(3, 100)
	if err := g.Transfer(0, 1, 30); err != nil {
		t.Fatal(err)
	}
	if s.Balance(0) != 70 || s.Balance(1) != 130 || s.Balance(2) != 100 {
		t.Fatalf("balances: %d %d %d", s.Balance(0), s.Balance(1), s.Balance(2))
	}
	if s.Total() != 300 {
		t.Fatalf("Total = %d, want 300", s.Total())
	}
	recs, _ := l.Replay(0)
	if len(recs) != 2 || recs[0].Txn != recs[1].Txn {
		t.Fatalf("WAL records: %+v", recs)
	}
	if recs[0].Delta != -30 || recs[1].Delta != 30 {
		t.Fatalf("deltas: %+v", recs)
	}
}

func TestWALFailureLeavesStateUntouched(t *testing.T) {
	g, l, s, sink := newLedger(2, 100)
	sink.fail = true
	if err := g.Transfer(0, 1, 10); err == nil {
		t.Fatal("expected WAL error")
	}
	if s.Balance(0) != 100 || s.Balance(1) != 100 {
		t.Fatal("shard state changed despite WAL failure")
	}
	if l.LastSeq() != 0 {
		t.Fatalf("failed txn consumed Seq: %d", l.LastSeq())
	}
	sink.fail = false
	if err := g.Transfer(0, 1, 10); err != nil {
		t.Fatal(err)
	}
	if l.LastSeq() != 2 {
		t.Fatalf("LastSeq = %d, want 2 (no gap from failed txn)", l.LastSeq())
	}
	if s.Balance(0) != 90 || s.Balance(1) != 110 {
		t.Fatal("retry after WAL failure did not apply cleanly")
	}
}

func TestValidationErrors(t *testing.T) {
	g, l, s, _ := newLedger(2, 100)
	cases := []struct {
		name     string
		from, to int
		amount   int64
	}{
		{"zero amount", 0, 1, 0},
		{"negative amount", 0, 1, -5},
		{"same shard", 1, 1, 10},
		{"from out of range", -1, 1, 10},
		{"to out of range", 0, 2, 10},
		{"insufficient funds", 0, 1, 101},
	}
	for _, c := range cases {
		if err := g.Transfer(c.from, c.to, c.amount); err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}
	if l.LastSeq() != 0 {
		t.Fatalf("rejected txns left WAL records: LastSeq = %d", l.LastSeq())
	}
	if s.Total() != 200 {
		t.Fatalf("rejected txns changed state: Total = %d", s.Total())
	}
}

func TestCheckpointTruncatesOnlyAppliedPrefix(t *testing.T) {
	g, l, s, _ := newLedger(2, 100)
	if err := g.Transfer(0, 1, 10); err != nil {
		t.Fatal(err)
	}
	if err := g.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	recs, _ := l.Replay(0)
	if len(recs) != 0 {
		t.Fatalf("fully applied WAL not truncated: %+v", recs)
	}
	// Crash after append leaves an unapplied record; checkpoint must
	// keep it, and recovery after the checkpoint must still fix state.
	g.CrashAt(CrashAfterAppend)
	if err := g.Transfer(0, 1, 20); err == nil {
		t.Fatal("expected crash error")
	}
	if err := g.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	recs, _ = l.Replay(0)
	if len(recs) != 2 {
		t.Fatalf("unapplied records wrongly truncated: %+v", recs)
	}
	// Simulated crash right after checkpoint: recover from WAL suffix.
	if _, err := g.Recover(); err != nil {
		t.Fatal(err)
	}
	if s.Balance(0) != 70 || s.Balance(1) != 130 || s.Total() != 200 {
		t.Fatalf("post-checkpoint recovery wrong: %d %d",
			s.Balance(0), s.Balance(1))
	}
}
