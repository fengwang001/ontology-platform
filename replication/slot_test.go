package replication

import (
	"bytes"
	"errors"
	"log"
	"sync"
	"testing"
)

func newTestSlot(t *testing.T, opts ...Option) (*Slot, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	all := append([]Option{WithLogger(logger)}, opts...)
	return NewSlot(all...), &buf
}

func equalU64(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func commitXIDs(txns []*Transaction) []uint64 {
	out := make([]uint64, len(txns))
	for i, txn := range txns {
		out[i] = txn.XID
	}
	return out
}

func mustAppend(t *testing.T, s *Slot, rec Record) {
	t.Helper()
	if err := s.Append(rec); err != nil {
		t.Fatalf("Append(%+v) unexpected error: %v", rec, err)
	}
}

// feedInterleaved 写入两个交错事务：
// T1 begin/data（1,2），T2 begin/data/commit（3,4,5），T1 data/commit（6,7）。
func feedInterleaved(t *testing.T, s *Slot) (commit1, commit2 uint64) {
	t.Helper()
	mustAppend(t, s, Record{LSN: 1, XID: 1, Type: Begin})
	mustAppend(t, s, Record{LSN: 2, XID: 1, Type: Data, Payload: "t1-a"})
	mustAppend(t, s, Record{LSN: 3, XID: 2, Type: Begin})
	mustAppend(t, s, Record{LSN: 4, XID: 2, Type: Data, Payload: "t2-a"})
	mustAppend(t, s, Record{LSN: 5, XID: 2, Type: Commit})
	mustAppend(t, s, Record{LSN: 6, XID: 1, Type: Data, Payload: "t1-b"})
	mustAppend(t, s, Record{LSN: 7, XID: 1, Type: Commit})
	return 7, 5
}

func TestEmittedAtomicTransactionOrder(t *testing.T) {
	s, _ := newTestSlot(t)
	c1, c2 := feedInterleaved(t, s)

	got := s.Emitted()
	if want := []uint64{2, 1}; !equalU64(commitXIDs(got), want) {
		t.Fatalf("emitted xids = %v, want %v (commit order)", commitXIDs(got), want)
	}

	t2 := got[0]
	if t2.BeginLSN != 3 || t2.CommitLSN != c2 || len(t2.Records) != 2 {
		t.Fatalf("t2 transaction wrong: %+v", t2)
	}
	if t2.Records[1].Payload != "t2-a" {
		t.Fatalf("t2 payload wrong: %q", t2.Records[1].Payload)
	}
	t1 := got[1]
	if t1.BeginLSN != 1 || t1.CommitLSN != c1 || len(t1.Records) != 3 {
		t.Fatalf("t1 transaction wrong: %+v", t1)
	}

	if got := s.Emitted(); len(got) != 0 {
		t.Fatalf("emitted queue not drained: %d", len(got))
	}
}

func TestConfirmAdvancesPositionsAndReclaims(t *testing.T) {
	s, _ := newTestSlot(t)
	c1, c2 := feedInterleaved(t, s)
	_ = s.Emitted()

	if err := s.Confirm(c2); err != nil {
		t.Fatalf("Confirm(%d): %v", c2, err)
	}
	if s.ConfirmedLSN() != c2 {
		t.Fatalf("confirmed=%d, want %d", s.ConfirmedLSN(), c2)
	}
	// T1 仍在进行（起点 1），重启位点被钉在 1，T1 的日志必须保留。
	if s.RestartLSN() != 1 {
		t.Fatalf("restart=%d, want 1 (pinned by open T1)", s.RestartLSN())
	}
	found := false
	for _, rec := range s.logs {
		if rec.LSN < c2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected retained records below confirmed=%d for open T1, kept=%v", c2, s.logs)
	}

	if err := s.Confirm(c1); err != nil {
		t.Fatalf("Confirm(%d): %v", c1, err)
	}
	if s.ConfirmedLSN() != c1 || s.RestartLSN() != c1 {
		t.Fatalf("positions = %d/%d, want %d/%d",
			s.ConfirmedLSN(), s.RestartLSN(), c1, c1)
	}
	// 早于重启位点 7 的日志全部回收（严格小于；7 的提交记录保留）。
	if len(s.logs) != 1 || s.logs[0].LSN != c1 {
		t.Fatalf("kept logs = %v, want only commit at %d", s.logs, c1)
	}

	// 幂等重复确认不改变任何状态。
	if err := s.Confirm(c1); err != nil {
		t.Fatalf("idempotent Confirm: %v", err)
	}
	if s.ConfirmedLSN() != c1 || s.RestartLSN() != c1 || len(s.logs) != 1 {
		t.Fatalf("state changed on idempotent confirm: %d/%d/%d",
			s.ConfirmedLSN(), s.RestartLSN(), len(s.logs))
	}
}

func TestRestartReemitsCommittedUnconfirmed(t *testing.T) {
	s, _ := newTestSlot(t)
	c1, c2 := feedInterleaved(t, s)
	if first := s.Emitted(); len(first) != 2 {
		t.Fatalf("initial emit count = %d, want 2", len(first))
	}

	// 只确认 T2；T1 已提交未确认。崩溃重启后必须重发 T1，且 T2 不重发（不漏不重）。
	if err := s.Confirm(c2); err != nil {
		t.Fatalf("Confirm(%d): %v", c2, err)
	}
	s.Restart()

	got := s.Emitted()
	if want := []uint64{1}; !equalU64(commitXIDs(got), want) {
		t.Fatalf("after restart xids = %v, want %v", commitXIDs(got), want)
	}
	if got[0].BeginLSN != 1 || got[0].CommitLSN != c1 || len(got[0].Records) != 3 {
		t.Fatalf("re-emitted T1 wrong: %+v", got[0])
	}
	if s.ConfirmedLSN() != c2 || s.RestartLSN() != 1 {
		t.Fatalf("positions after restart: confirmed=%d restart=%d, want %d/1",
			s.ConfirmedLSN(), s.RestartLSN(), c2)
	}

	// 重启后确认 T1、再次重启，不应发出任何事务。
	if err := s.Confirm(c1); err != nil {
		t.Fatalf("Confirm(%d) after restart: %v", c1, err)
	}
	s.Restart()
	if got := s.Emitted(); len(got) != 0 {
		t.Fatalf("expected no re-emit after all confirmed, got %d", len(got))
	}
}

func TestRestartReplaysInProgressAndAbortsNotReemitted(t *testing.T) {
	s, _ := newTestSlot(t)
	mustAppend(t, s, Record{LSN: 1, XID: 10, Type: Begin})
	mustAppend(t, s, Record{LSN: 2, XID: 10, Type: Data, Payload: "open"})
	mustAppend(t, s, Record{LSN: 3, XID: 20, Type: Begin})
	mustAppend(t, s, Record{LSN: 4, XID: 20, Type: Abort})
	mustAppend(t, s, Record{LSN: 5, XID: 30, Type: Begin})
	mustAppend(t, s, Record{LSN: 6, XID: 30, Type: Commit})

	s.Restart()

	got := s.Emitted()
	if want := []uint64{30}; !equalU64(commitXIDs(got), want) {
		t.Fatalf("only committed T30 should be re-emitted, got %v", commitXIDs(got))
	}

	// 进行中事务 T10 从保留日志重建，重启后仍可继续追加并提交；T20 已中止不可追加。
	mustAppend(t, s, Record{LSN: 7, XID: 10, Type: Data, Payload: "after-restart"})
	mustAppend(t, s, Record{LSN: 8, XID: 10, Type: Commit})
	if err := s.Append(Record{LSN: 9, XID: 20, Type: Data}); !errors.Is(err, ErrIllegalRecord) {
		t.Fatalf("data for aborted T20 error = %v, want ErrIllegalRecord", err)
	}
	got = s.Emitted()
	if want := []uint64{10}; !equalU64(commitXIDs(got), want) {
		t.Fatalf("T10 after restart xids = %v, want %v", commitXIDs(got), want)
	}
	if len(got[0].Records) != 3 || got[0].Records[2].Payload != "after-restart" {
		t.Fatalf("T10 records after restart wrong: %+v", got[0].Records)
	}
}
