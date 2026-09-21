package ledger

import (
	"errors"
	"testing"
)

// TestCheckpointKeepsFirstUnappliedRecord 回归：Checkpoint 曾以
// appliedSeq+1 截断，把第一条未应用的记录（Seq == appliedSeq+1）
// 一并截掉，崩溃恢复因此永久丢账。修复后只截到 appliedSeq。
func TestCheckpointKeepsFirstUnappliedRecord(t *testing.T) {
	g, l, s, _ := newLedger(2, 100)
	if err := g.Transfer(0, 1, 10); err != nil {
		t.Fatal(err)
	}
	g.CrashAt(CrashAfterAppend)
	if err := g.Transfer(0, 1, 20); !errors.Is(err, ErrCrashed) {
		t.Fatalf("want ErrCrashed, got %v", err)
	}
	if err := g.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	// 未应用的两条记录（Seq 3,4）必须完整保留，一条都不能少。
	recs, _ := l.Replay(0)
	if len(recs) != 2 || recs[0].Seq != 3 || recs[1].Seq != 4 {
		t.Fatalf("unapplied records truncated: %+v", recs)
	}
	if _, err := g.Recover(); err != nil {
		t.Fatal(err)
	}
	if s.Balance(0) != 70 || s.Balance(1) != 130 {
		t.Fatalf("balances = %d,%d, want 70,130", s.Balance(0), s.Balance(1))
	}
}

// TestRecoverAdvancesAppliedSeq 回归：Recover 曾不推进 appliedSeq，
// 导致 CrashAfterApply（元数据未更新）恢复后，已应用的记录永远无法
// 被 Checkpoint 截断。修复后 Recover 把 appliedSeq 推进到 LastSeq。
func TestRecoverAdvancesAppliedSeq(t *testing.T) {
	g, l, s, _ := newLedger(2, 100)
	if err := g.Transfer(0, 1, 10); err != nil {
		t.Fatal(err)
	}
	g.CrashAt(CrashAfterApply)
	if err := g.Transfer(0, 1, 20); !errors.Is(err, ErrCrashed) {
		t.Fatalf("want ErrCrashed, got %v", err)
	}
	if _, err := g.Recover(); err != nil {
		t.Fatal(err)
	}
	if g.AppliedSeq() != l.LastSeq() {
		t.Fatalf("AppliedSeq = %d, want LastSeq %d", g.AppliedSeq(), l.LastSeq())
	}
	// 全部记录已确认应用，Checkpoint 应能完全截断 WAL。
	if err := g.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if recs, _ := l.Replay(0); len(recs) != 0 {
		t.Fatalf("checkpoint after recover left %d records, want 0", len(recs))
	}
	if s.Total() != 200 {
		t.Fatalf("Total = %d, want 200", s.Total())
	}
}
