package ledger

import (
	"errors"
	"testing"

	"ontology/shard"
	"ontology/wal"
)

type memSink struct{ err error }

func (m *memSink) Write(p []byte) (int, error) {
	if m.err != nil {
		return 0, m.err
	}
	return len(p), nil
}

func newLedger(n int, initial int64) (*Ledger, *wal.Log, *shard.Set, *memSink) {
	sink := &memSink{}
	l := wal.New(sink)
	s := shard.New(n, initial)
	return New(l, s), l, s, sink
}

func balancesOf(s *shard.Set) []int64 {
	out := make([]int64, s.Len())
	for i := range out {
		out[i] = s.Balance(i)
	}
	return out
}

func TestTransferValidation(t *testing.T) {
	g, l, s, _ := newLedger(3, 100)
	cases := []struct {
		from, to int
		amount   int64
	}{
		{0, 1, 0},   // amount <= 0
		{0, 1, -5},  // amount < 0
		{1, 1, 10},  // from == to
		{-1, 1, 10}, // from 越界
		{0, 3, 10},  // to 越界
		{0, 1, 101}, // 余额不足
	}
	for i, c := range cases {
		if err := g.Transfer(c.from, c.to, c.amount); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
	if l.LastSeq() != 0 {
		t.Fatalf("rejected txns touched WAL: LastSeq = %d", l.LastSeq())
	}
	if s.Total() != 300 {
		t.Fatalf("rejected txns changed state: Total = %d", s.Total())
	}
	recs, _ := l.Replay(0)
	if len(recs) != 0 {
		t.Fatalf("rejected txns left %d WAL records", len(recs))
	}
}

func TestSinkFailureLeavesStateUntouched(t *testing.T) {
	g, l, s, sink := newLedger(2, 100)
	sink.err = errors.New("disk on fire")
	if err := g.Transfer(0, 1, 10); err == nil {
		t.Fatal("expected sink error")
	}
	if s.Balance(0) != 100 || s.Balance(1) != 100 {
		t.Fatalf("state changed on sink failure: %d,%d", s.Balance(0), s.Balance(1))
	}
	if l.LastSeq() != 0 {
		t.Fatalf("failed txn consumed Seq: %d", l.LastSeq())
	}
	// 落盘恢复后同一协调器可继续工作，且 Seq 从 1 开始无空洞。
	sink.err = nil
	if err := g.Transfer(0, 1, 10); err != nil {
		t.Fatal(err)
	}
	recs, _ := l.Replay(0)
	if len(recs) != 2 || recs[0].Seq != 1 || recs[1].Seq != 2 {
		t.Fatalf("records = %+v", recs)
	}
}

func TestCrashRecoveryAtAllPoints(t *testing.T) {
	points := []CrashPoint{CrashAfterAppend, CrashAfterDebit, CrashAfterApply}
	for _, p := range points {
		// 基线：不崩溃的同样两笔转账。
		base, _, bs, _ := newLedger(3, 100)
		// 被测：第二笔转账在 p 处崩溃。
		got, _, gs, _ := newLedger(3, 100)
		for _, g := range []*Ledger{base, got} {
			if err := g.Transfer(0, 1, 10); err != nil {
				t.Fatal(err)
			}
		}
		got.CrashAt(p)
		if err := got.Transfer(1, 2, 25); !errors.Is(err, ErrCrashed) {
			t.Fatalf("point %d: want ErrCrashed, got %v", p, err)
		}
		if err := base.Transfer(1, 2, 25); err != nil {
			t.Fatal(err)
		}
		if _, err := got.Recover(); err != nil {
			t.Fatalf("point %d: recover: %v", p, err)
		}
		want, have := balancesOf(bs), balancesOf(gs)
		for i := range want {
			if want[i] != have[i] {
				t.Fatalf("point %d: shard %d = %d, want %d", p, i, have[i], want[i])
			}
		}
		if gs.Total() != 300 {
			t.Fatalf("point %d: Total = %d, want 300", p, gs.Total())
		}
	}
}

func TestRecoverIdempotent(t *testing.T) {
	g, _, s, _ := newLedger(2, 100)
	g.CrashAt(CrashAfterDebit)
	if err := g.Transfer(0, 1, 40); !errors.Is(err, ErrCrashed) {
		t.Fatalf("want ErrCrashed, got %v", err)
	}
	n1, err := g.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if n1 == 0 {
		t.Fatal("first Recover should apply the missing credit")
	}
	if s.Balance(0) != 60 || s.Balance(1) != 140 {
		t.Fatalf("balances = %d,%d", s.Balance(0), s.Balance(1))
	}
	n2, err := g.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Fatalf("second Recover replayed %d, want 0", n2)
	}
	if s.Balance(0) != 60 || s.Balance(1) != 140 || s.Total() != 200 {
		t.Fatal("state changed after repeated Recover")
	}
}
