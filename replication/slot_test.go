package replication

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func openSlot(t *testing.T, dir string, opts ...Option) *Slot {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), dir), opts...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustAppend(t *testing.T, s *Slot, r Record) *Transaction {
	t.Helper()
	txn, err := s.Append(context.Background(), r)
	if err != nil {
		t.Fatalf("Append(%+v): %v", r, err)
	}
	return txn
}

func appendTxn(t *testing.T, s *Slot, base, xid uint64, aborted bool) *Transaction {
	t.Helper()
	mustAppend(t, s, begin(base, xid))
	mustAppend(t, s, data(base+1, xid, []byte{byte(xid)}))
	if aborted {
		mustAppend(t, s, abort(base+2, xid))
		return nil
	}
	return mustAppend(t, s, commit(base+2, xid))
}

// 崩溃重启后：已提交未确认事务按提交顺序重新发出一次；已确认的不重发；中止的不重发。
func TestSlotRestartReemitsCommittedUnconfirmed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "slot")
	ctx := context.Background()

	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	txn1 := appendTxn(t, s, 1, 1, false) // commit LSN 3
	if txn1.CommitLSN != 3 {
		t.Fatalf("commit lsn = %d", txn1.CommitLSN)
	}
	appendTxn(t, s, 4, 2, true)          // abort，丢弃
	txn3 := appendTxn(t, s, 7, 3, false) // commit LSN 9，未确认
	if txn3.CommitLSN != 9 {
		t.Fatalf("txn3 commit lsn = %d", txn3.CommitLSN)
	}
	if err := s.Confirm(ctx, 3); err != nil {
		t.Fatalf("Confirm(3): %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	reemitted := s2.Reemitted()
	if len(reemitted) != 1 {
		t.Fatalf("reemitted %d txns: %+v", len(reemitted), reemitted)
	}
	if reemitted[0].XID != 3 || reemitted[0].CommitLSN != 9 {
		t.Fatalf("reemitted = %+v, want only txn 3 @9", reemitted[0])
	}
	if restart, confirmed := s2.Positions(); !(restart <= confirmed) || confirmed != 3 || restart != 3 {
		t.Fatalf("positions after restart = (%d,%d), want restart=3 confirmed=3", restart, confirmed)
	}
	if s2.NextLSN() != 10 {
		t.Fatalf("NextLSN = %d, want 10", s2.NextLSN())
	}

	// 重启后恢复的槽可以继续追加与确认，且 LSN 序号接得上。
	txn4 := appendTxn(t, s2, 10, 4, false)
	if txn4.CommitLSN != 12 {
		t.Fatalf("commit lsn = %d, want 12", txn4.CommitLSN)
	}
	// txn3@9 仍未确认；确认必须恰好落在边界上，先确认 9 再确认 12。
	if err := s2.Confirm(ctx, 9); err != nil {
		t.Fatalf("Confirm(9): %v", err)
	}
	if err := s2.Confirm(ctx, 12); err != nil {
		t.Fatalf("Confirm(12): %v", err)
	}
	if restart, confirmed := s2.Positions(); restart != confirmed || confirmed != 12 {
		t.Fatalf("positions = (%d,%d), want (12,12)", restart, confirmed)
	}
	_ = s2.Close()

	// 再次重启：没有任何待重发事务。
	s3, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("reopen2: %v", err)
	}
	if pending := s3.Reemitted(); len(pending) != 0 {
		t.Fatalf("reemitted after full confirm = %+v", pending)
	}
}

// 确认推进后，早于重启位点的记录从 WAL 文件中物理回收。
func TestSlotWALReclaimedOnDisk(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "slot")
	ctx := context.Background()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	appendTxn(t, s, 1, 1, false)
	appendTxn(t, s, 4, 2, false) // 进行中/已提交都会保留起点
	mustAppend(t, s, begin(7, 3))
	if err := s.Confirm(ctx, 6); err != nil {
		t.Fatalf("Confirm(6): %v", err)
	}
	_ = s.Close()

	f, err := os.Open(filepath.Join(dir, walName))
	if err != nil {
		t.Fatalf("open wal: %v", err)
	}
	defer f.Close()
	records, err := readRecords(f)
	if err != nil {
		t.Fatalf("readRecords: %v", err)
	}
	for _, r := range records {
		if r.LSN < 6 {
			t.Fatalf("record LSN %d older than restart survived compaction", r.LSN)
		}
	}
	if len(records) != 2 || records[0].LSN != 6 || records[1].LSN != 7 {
		t.Fatalf("retained on-disk records = %+v, want boundary commit@6 and begin@7", records)
	}

	meta, ok, err := readMetadata(dir)
	if err != nil || !ok || meta.ConfirmedLSN != 6 || meta.RestartLSN != 6 {
		t.Fatalf("metadata = %+v ok=%v err=%v", meta, ok, err)
	}
}

// Slot 层拒绝各类非法操作，原因可区分且状态不变。
func TestSlotRejections(t *testing.T) {
	s := openSlot(t, "slot")
	ctx := context.Background()

	appendTxn(t, s, 1, 1, false) // commit @3
	mustAppend(t, s, begin(4, 2))

	restart, confirmed := s.Positions()

	if _, err := s.Append(ctx, Record{LSN: 5, XID: 0, Kind: KindData}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("zero xid: %v", err)
	}
	if _, err := s.Append(ctx, Record{LSN: 5, XID: 9, Kind: KindData}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("data without begin: %v", err)
	}
	if _, err := s.Append(ctx, begin(1, 1)); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("stale LSN: %v", err)
	}
	if _, err := s.Append(ctx, Record{LSN: 5, XID: 2, Kind: Kind(42)}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("bad kind: %v", err)
	}
	if err := s.Confirm(ctx, 2); !errors.Is(err, ErrInvalidConfirm) {
		t.Fatalf("confirm mid-txn: %v", err)
	}
	if err := s.Confirm(ctx, 99); !errors.Is(err, ErrInvalidConfirm) {
		t.Fatalf("confirm unknown: %v", err)
	}
	if err := s.Confirm(ctx, 0); err != nil { // 初始位点 0，幂等成功
		t.Fatalf("idempotent Confirm(0): %v", err)
	}
	if r, c := s.Positions(); r != restart || c != confirmed {
		t.Fatalf("positions changed after rejections: want (%d,%d), got (%d,%d)", restart, confirmed, r, c)
	}

	// 回退确认必须拒绝。
	if err := s.Confirm(ctx, 3); err != nil {
		t.Fatalf("Confirm(3): %v", err)
	}
	if err := s.Confirm(ctx, 0); !errors.Is(err, ErrConfirmRewound) {
		t.Fatalf("rewind to 0: %v", err)
	}

	// 进行中事务超限。
	limited := openSlot(t, "limited", WithMaxInProgress(1))
	mustAppend(t, limited, begin(1, 1))
	if _, err := limited.Append(ctx, begin(2, 2)); !errors.Is(err, ErrTooManyInProgress) {
		t.Fatalf("limit: %v", err)
	}
}

// 并发追加/确认/读取：位点始终单调且 restart <= confirmed。
func TestSlotConcurrentInvariants(t *testing.T) {
	s := openSlot(t, "concurrent")
	ctx := context.Background()
	const n = 40

	var wg sync.WaitGroup
	stop := make(chan struct{})
	appendDone := make(chan struct{})
	var failuresMu sync.Mutex
	var failures []string
	addFailure := func(format string, args ...any) {
		failuresMu.Lock()
		failures = append(failures, fmt.Sprintf(format, args...))
		failuresMu.Unlock()
	}

	// 读者：反复检查不变量与单调性。
	wg.Add(1)
	go func() {
		defer wg.Done()
		lastRestart, lastConfirmed := uint64(0), uint64(0)
		for {
			select {
			case <-stop:
				return
			default:
				r, c := s.Positions()
				if r > c || r < lastRestart || c < lastConfirmed {
					addFailure("invariant violated: (%d,%d) previous (%d,%d)", r, c, lastRestart, lastConfirmed)
					return
				}
				lastRestart, lastConfirmed = r, c
			}
		}
	}()

	// 提交者（串行化 Append，保证 LSN 顺序合法）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(appendDone)
		for i := uint64(1); i <= n; i++ {
			for _, r := range []Record{
				begin(3*i-2, i),
				data(3*i-1, i, []byte("p")),
				commit(3*i, i),
			} {
				if _, err := s.Append(ctx, r); err != nil {
					if !errors.Is(err, ErrSlotClosed) {
						addFailure("append %+v: %v", r, err)
					}
					return
				}
			}
		}
	}()

	// 确认者：尝试推进，非法位点被拒属正常。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-appendDone:
				return
			default:
			}
			for i := uint64(1); i <= n; i++ {
				_ = s.Confirm(ctx, 3*i)
			}
		}
	}()

	// 等提交全部完成，再补齐确认（并发确认可能早于对应提交而被拒）。
	<-appendDone
	if err := s.Confirm(ctx, 3*n); err != nil && !errors.Is(err, ErrConfirmRewound) {
		addFailure("final confirm: %v", err)
	}
	close(stop)
	wg.Wait()

	for _, msg := range failures {
		t.Error(msg)
	}
	if restart, confirmed := s.Positions(); confirmed != 3*n || restart != confirmed {
		t.Fatalf("final positions = (%d,%d), want (%d,%d)", restart, confirmed, 3*n, 3*n)
	}
}

// 崩溃留下的尾部撕裂帧被忽略，槽仍能恢复到最近一次完整记录。
func TestSlotTornTailIgnored(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "slot")
	ctx := context.Background()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	appendTxn(t, s, 1, 1, false) // commit @3
	_ = s.Close()

	// 追加一个“撕裂帧”：帧头声称有 payload 但文件就此结束。
	f, err := os.OpenFile(filepath.Join(dir, walName), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open wal: %v", err)
	}
	frame := encodeRecord(begin(4, 2))
	if _, err := f.Write(frame[:len(frame)-3]); err != nil {
		t.Fatalf("write torn: %v", err)
	}
	_ = f.Close()

	s2, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("reopen with torn tail: %v", err)
	}
	if s2.NextLSN() != 4 {
		t.Fatalf("NextLSN = %d, want 4 (torn frame ignored)", s2.NextLSN())
	}
	pending := s2.Reemitted()
	if len(pending) != 1 || pending[0].XID != 1 {
		t.Fatalf("pending = %+v", pending)
	}
}

// 日志中打印输入、两个位点、发出事务与判定依据。
func TestSlotStructuredLogging(t *testing.T) {
	var buf bytes.Buffer
	s := openSlot(t, "logged", WithLogger(&buf))
	ctx := context.Background()
	mustAppend(t, s, begin(1, 1))
	mustAppend(t, s, data(2, 1, []byte("hi")))
	mustAppend(t, s, commit(3, 1))
	if err := s.Confirm(ctx, 3); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	_, err := s.Append(ctx, Record{LSN: 4, XID: 0, Kind: KindBegin})
	if !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("expected rejection, got %v", err)
	}

	out := buf.String()
	for _, want := range []string{`"event":"append"`, `"confirmed_lsn"`, `"restart_lsn"`, `"emitted"`, `"reason"`, `append_rejected`, `"COMMIT"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q in:\n%s", want, out)
		}
	}
	t.Logf("captured logs:\n%s", out)
}
