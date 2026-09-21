package ledger

import (
	"errors"
	"testing"
)

// 回归：Checkpoint 曾按 appliedSeq+1 截断，把第一条未应用的记录
// 一并丢弃，崩溃后 Recover 永远补不回该 Txn。
// 根因修复：Truncate 只截到 appliedSeq。
func TestCheckpointKeepsFirstUnappliedRecordRegression(t *testing.T) {
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
	// 未应用的两条记录（Seq 3、4）必须完整保留，尤其是 Seq 3。
	recs, _ := l.Replay(0)
	if len(recs) != 2 || recs[0].Seq != 3 || recs[1].Seq != 4 {
		t.Fatalf("records after checkpoint = %+v, want Seq 3,4", recs)
	}
	if _, err := g.Recover(); err != nil {
		t.Fatal(err)
	}
	if s.Balance(0) != 70 || s.Balance(1) != 130 || s.Total() != 200 {
		t.Fatalf("balances = %d,%d", s.Balance(0), s.Balance(1))
	}
}

// 回归：Recover 曾不推进 appliedSeq，CrashAfterApply 丢失的元数据
// 无法补回，导致 Recover 后 Checkpoint 截不掉已应用的记录。
// 根因修复：Recover 末尾把 appliedSeq 推进到 LastSeq。
func TestRecoverAdvancesAppliedSeqRegression(t *testing.T) {
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
	// 与不崩溃时一致：appliedSeq 覆盖全部已落盘记录。
	if g.AppliedSeq() != l.LastSeq() {
		t.Fatalf("AppliedSeq = %d, want %d", g.AppliedSeq(), l.LastSeq())
	}
	if err := g.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if recs, _ := l.Replay(0); len(recs) != 0 {
		t.Fatalf("checkpoint left %d records, want 0", len(recs))
	}
	if s.Balance(0) != 70 || s.Balance(1) != 130 || s.Total() != 200 {
		t.Fatalf("balances = %d,%d", s.Balance(0), s.Balance(1))
	}
}
