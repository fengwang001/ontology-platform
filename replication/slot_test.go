package replication

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func begin(l LSN, txn uint64) Record { return Record{LSN: l, TxnID: txn, Type: RecordBegin} }

func data(l LSN, txn uint64, payload string) Record {
	return Record{LSN: l, TxnID: txn, Type: RecordData, Payload: payload}
}

func commit(l LSN, txn uint64) Record { return Record{LSN: l, TxnID: txn, Type: RecordCommit} }

func abort(l LSN, txn uint64) Record { return Record{LSN: l, TxnID: txn, Type: RecordAbort} }

// logPositions 在测试日志中打印两个位点与回收位置。
func logPositions(t *testing.T, s *Slot, why string) {
	t.Helper()
	restart, confirmed := s.Positions()
	t.Logf("%s: restartLSN=%d confirmedLSN=%d retainedFrom=%d", why, restart, confirmed, s.RetainedFrom())
}

// mustAppend 追加记录并要求成功，打印输入与发出的事务。
func mustAppend(t *testing.T, s *Slot, rec Record) *Transaction {
	t.Helper()
	txn, err := s.Append(rec)
	if err != nil {
		t.Fatalf("append %+v: unexpected error: %v", rec, err)
	}
	if txn != nil {
		t.Logf("append %+v -> emit txn id=%d start=%d commit=%d records=%d",
			rec, txn.ID, txn.StartLSN, txn.CommitLSN, len(txn.Records))
	} else {
		t.Logf("append %+v -> no emit", rec)
	}
	return txn
}

// mustConfirm 确认位点并要求成功。
func mustConfirm(t *testing.T, s *Slot, lsn LSN) {
	t.Helper()
	if err := s.Confirm(lsn); err != nil {
		t.Fatalf("confirm %d: unexpected error: %v", lsn, err)
	}
	t.Logf("confirm %d -> ok", lsn)
}

// state 是用于校验“被拒绝的操作不改变状态”的快照。
type state struct {
	restart, confirmed, retainedFrom LSN
}

func snapshot(s *Slot) state {
	restart, confirmed := s.Positions()
	return state{restart: restart, confirmed: confirmed, retainedFrom: s.RetainedFrom()}
}

func requireUnchanged(t *testing.T, s *Slot, before state, why string) {
	t.Helper()
	after := snapshot(s)
	if after != before {
		t.Fatalf("%s: state changed after rejection: before=%+v after=%+v", why, before, after)
	}
	t.Logf("%s: rejected, state unchanged (restart=%d confirmed=%d retainedFrom=%d)",
		why, after.restart, after.confirmed, after.retainedFrom)
}

// TestRestartReemitsUnconfirmedCommitted 验证崩溃重启后重新发出
// 已提交但未确认的事务，且不重不漏。
func TestRestartReemitsUnconfirmedCommitted(t *testing.T) {
	s := NewSlot(8)

	// 事务 1：提交并确认。
	mustAppend(t, s, begin(1, 1))
	mustAppend(t, s, data(2, 1, "a"))
	txn1 := mustAppend(t, s, commit(3, 1))
	// 事务 2 与 3 交错：事务 2 提交但未确认，事务 3 提交但未确认。
	mustAppend(t, s, begin(4, 2))
	mustAppend(t, s, begin(5, 3))
	mustAppend(t, s, data(6, 2, "b"))
	mustAppend(t, s, data(7, 3, "c"))
	txn2 := mustAppend(t, s, commit(8, 2))
	txn3 := mustAppend(t, s, commit(9, 3))

	mustConfirm(t, s, txn1.CommitLSN)
	logPositions(t, s, "after confirm txn1")

	emitted := s.Recover()
	t.Logf("recover emitted %d txns", len(emitted))
	logPositions(t, s, "after recover")

	want := []Transaction{*txn2, *txn3}
	if !reflect.DeepEqual(emitted, want) {
		t.Fatalf("recover emitted mismatch:\n got=%+v\nwant=%+v", emitted, want)
	}

	// 重启后确认可继续推进到事务 2、3 的提交位点。
	mustConfirm(t, s, txn2.CommitLSN)
	mustConfirm(t, s, txn3.CommitLSN)
	logPositions(t, s, "after confirming re-emitted txns")

	// 再次重启：全部已确认，不应再发出任何事务。
	if again := s.Recover(); len(again) != 0 {
		t.Fatalf("second recover emitted %d txns, want 0", len(again))
	}
	t.Logf("second recover emitted nothing: no duplicates, no losses")
}

// TestAbortNotReemitted 验证中止事务被丢弃，重启后也不会重发。
func TestAbortNotReemitted(t *testing.T) {
	s := NewSlot(8)

	mustAppend(t, s, begin(1, 1))
	mustAppend(t, s, data(2, 1, "x"))
	mustAppend(t, s, abort(3, 1))
	mustAppend(t, s, begin(4, 2))
	mustAppend(t, s, data(5, 2, "y"))
	txn2 := mustAppend(t, s, commit(6, 2))
	logPositions(t, s, "before recover")

	emitted := s.Recover()
	if len(emitted) != 1 || emitted[0].ID != txn2.ID {
		t.Fatalf("recover emitted %+v, want only txn %d", emitted, txn2.ID)
	}
	for _, txn := range emitted {
		if txn.ID == 1 {
			t.Fatalf("aborted txn 1 was re-emitted: %+v", txn)
		}
	}
	t.Logf("aborted txn 1 discarded and not re-emitted; committed txn 2 re-emitted")
}

// TestInvalidRecords 验证各类非法日志记录被拒绝且原因可区分、状态不变。
func TestInvalidRecords(t *testing.T) {
	cases := []struct {
		name string
		seed func(t *testing.T, s *Slot)
		rec  Record
		want error
	}{
		{
			name: "unknown record type",
			rec:  Record{LSN: 1, TxnID: 1, Type: RecordType(99)},
			want: ErrInvalidRecord,
		},
		{
			name: "lsn not increasing",
			seed: func(t *testing.T, s *Slot) { mustAppend(t, s, begin(5, 1)) },
			rec:  data(5, 1, "dup"),
			want: ErrInvalidRecord,
		},
		{
			name: "data without begin",
			rec:  data(1, 7, "orphan"),
			want: ErrInvalidRecord,
		},
		{
			name: "commit without begin",
			rec:  commit(1, 7),
			want: ErrInvalidRecord,
		},
		{
			name: "abort without begin",
			rec:  abort(1, 7),
			want: ErrInvalidRecord,
		},
		{
			name: "duplicate begin",
			seed: func(t *testing.T, s *Slot) { mustAppend(t, s, begin(1, 1)) },
			rec:  begin(2, 1),
			want: ErrInvalidRecord,
		},
		{
			name: "too many in progress",
			seed: func(t *testing.T, s *Slot) {
				mustAppend(t, s, begin(1, 1))
				mustAppend(t, s, begin(2, 2))
			},
			rec:  begin(3, 3),
			want: ErrTooManyInProgress,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSlot(2)
			if tc.seed != nil {
				tc.seed(t, s)
			}
			before := snapshot(s)
			_, err := s.Append(tc.rec)
			if !errors.Is(err, tc.want) {
				t.Fatalf("append %+v: got error %v, want reason %v", tc.rec, err, tc.want)
			}
			t.Logf("append %+v rejected: %v", tc.rec, err)
			requireUnchanged(t, s, before, tc.name)
		})
	}

	// 不同原因之间必须可区分。
	if errors.Is(ErrTooManyInProgress, ErrInvalidRecord) || errors.Is(ErrConfirmRegression, ErrConfirmNotOnBoundary) {
		t.Fatal("rejection reasons must be distinguishable")
	}
}

// TestConfirmRejections 验证确认回退与确认未落在事务边界均被拒绝且状态不变。
func TestConfirmRejections(t *testing.T) {
	s := NewSlot(4)

	mustAppend(t, s, begin(1, 1))
	mustAppend(t, s, data(2, 1, "a"))
	txn1 := mustAppend(t, s, commit(3, 1))
	mustAppend(t, s, begin(4, 2))
	txn2 := mustAppend(t, s, commit(5, 2))

	// 确认未落在任何已发出事务的提交位点上（落在数据记录上）。
	before := snapshot(s)
	if err := s.Confirm(2); !errors.Is(err, ErrConfirmNotOnBoundary) {
		t.Fatalf("confirm 2: got %v, want %v", err, ErrConfirmNotOnBoundary)
	} else {
		t.Logf("confirm 2 rejected (not a commit boundary): %v", err)
	}
	requireUnchanged(t, s, before, "confirm on data record")

	// 确认落在尚未发出的位点上。
	before = snapshot(s)
	if err := s.Confirm(100); !errors.Is(err, ErrConfirmNotOnBoundary) {
		t.Fatalf("confirm 100: got %v, want %v", err, ErrConfirmNotOnBoundary)
	} else {
		t.Logf("confirm 100 rejected (no such commit): %v", err)
	}
	requireUnchanged(t, s, before, "confirm beyond emitted")

	// 正常推进到事务 1 的提交位点。
	mustConfirm(t, s, txn1.CommitLSN)
	logPositions(t, s, "after confirm txn1")

	// 确认回退：小于当前确认位点。
	before = snapshot(s)
	if err := s.Confirm(1); !errors.Is(err, ErrConfirmRegression) {
		t.Fatalf("confirm 1: got %v, want %v", err, ErrConfirmRegression)
	} else {
		t.Logf("confirm 1 rejected (regression): %v", err)
	}
	requireUnchanged(t, s, before, "confirm regression")

	// 确认不前进：等于当前确认位点同样拒绝。
	before = snapshot(s)
	if err := s.Confirm(txn1.CommitLSN); !errors.Is(err, ErrConfirmRegression) {
		t.Fatalf("confirm %d again: got %v, want %v", txn1.CommitLSN, err, ErrConfirmRegression)
	} else {
		t.Logf("confirm %d rejected (no advance): %v", txn1.CommitLSN, err)
	}
	requireUnchanged(t, s, before, "confirm no advance")

	// 跳过事务 2 直接确认不存在的位点仍被拒绝；确认事务 2 成功。
	mustConfirm(t, s, txn2.CommitLSN)
	logPositions(t, s, "after confirm txn2")
}

// TestRetentionAndReclaim 验证重启位点取确认位点与未确认事务起点的较小值，
// 且早于重启位点的日志被回收。
func TestRetentionAndReclaim(t *testing.T) {
	s := NewSlot(8)

	// 事务 1 起点为 1，提交于 3；事务 2 起点为 4，长时间不提交。
	mustAppend(t, s, begin(1, 1))
	mustAppend(t, s, data(2, 1, "a"))
	txn1 := mustAppend(t, s, commit(3, 1))
	mustAppend(t, s, begin(4, 2))
	mustAppend(t, s, data(5, 2, "b"))

	// 确认事务 1 后，事务 2 仍在进行：重启位点 = min(3, 4) = 3，
	// 早于 3 的日志（LSN 1、2）被回收。
	mustConfirm(t, s, txn1.CommitLSN)
	logPositions(t, s, "txn2 in progress pins restart")
	restart, confirmed := s.Positions()
	if restart != 3 || confirmed != 3 {
		t.Fatalf("got restart=%d confirmed=%d, want restart=3 confirmed=3", restart, confirmed)
	}
	if got := s.RetainedFrom(); got != 3 {
		t.Fatalf("retainedFrom=%d, want 3 (records before restart reclaimed)", got)
	}

	// 事务 2 提交后未确认：重启位点仍被其起点 4 钉住之前先取 min(3,4)=3；
	// 确认事务 2 后，重启位点推进到确认位点 6，日志全部回收。
	txn2 := mustAppend(t, s, commit(6, 2))
	logPositions(t, s, "txn2 committed but unconfirmed")
	mustConfirm(t, s, txn2.CommitLSN)
	logPositions(t, s, "all confirmed, log reclaimed")
	restart, confirmed = s.Positions()
	if restart != 6 || confirmed != 6 {
		t.Fatalf("got restart=%d confirmed=%d, want both 6", restart, confirmed)
	}
	if got := s.RetainedFrom(); got != 6 {
		t.Fatalf("retainedFrom=%d, want 6", got)
	}
	t.Logf("rule verified: restart=min(confirmed, open/unconfirmed txn starts); reclaim below restart")
}

// TestConcurrent 并发调用追加、确认与读位点，验证两个位点单调且重启位点
// 不大于确认位点（配合 -race 运行）。
func TestConcurrent(t *testing.T) {
	s := NewSlot(64)

	const txns = 200
	var wg sync.WaitGroup

	// 单写者追加：每个事务 begin+commit，LSN 严格递增。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < txns; i++ {
			id := uint64(i + 1)
			base := LSN(2*i + 1)
			if _, err := s.Append(begin(base, id)); err != nil {
				t.Errorf("append begin: %v", err)
				return
			}
			if _, err := s.Append(commit(base+1, id)); err != nil {
				t.Errorf("append commit: %v", err)
				return
			}
		}
	}()

	// 确认者：依次确认每个提交位点。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < txns; i++ {
			commitLSN := LSN(2*i + 2)
			for {
				err := s.Confirm(commitLSN)
				if err == nil {
					break
				}
				if !errors.Is(err, ErrConfirmNotOnBoundary) {
					t.Errorf("confirm %d: %v", commitLSN, err)
					return
				}
			}
		}
	}()

	// 读者：持续读取两个位点，校验单调性与先后关系。
	wg.Add(1)
	go func() {
		defer wg.Done()
		var lastRestart, lastConfirmed LSN
		for i := 0; i < 2000; i++ {
			restart, confirmed := s.Positions()
			if restart > confirmed {
				t.Errorf("invariant violated: restart=%d > confirmed=%d", restart, confirmed)
				return
			}
			if restart < lastRestart || confirmed < lastConfirmed {
				t.Errorf("positions regressed: (%d,%d) -> (%d,%d)",
					lastRestart, lastConfirmed, restart, confirmed)
				return
			}
			lastRestart, lastConfirmed = restart, confirmed
		}
	}()

	wg.Wait()
	restart, confirmed := s.Positions()
	t.Logf("final: restart=%d confirmed=%d retainedFrom=%d", restart, confirmed, s.RetainedFrom())
	if confirmed != LSN(2*txns) {
		t.Fatalf("confirmed=%d, want %d", confirmed, 2*txns)
	}
}

// TestDeterministic 同一输入序列反复计算得到完全相同的输出。
func TestDeterministic(t *testing.T) {
	script := []Record{
		begin(1, 1), begin(2, 2), data(3, 1, "a"), abort(4, 2),
		data(5, 1, "b"), commit(6, 1), begin(7, 3), data(8, 3, "c"),
		commit(9, 3),
	}
	confirms := []LSN{6, 9}

	run := func() string {
		s := NewSlot(4)
		var out string
		for _, rec := range script {
			txn, err := s.Append(rec)
			out += fmt.Sprintf("append %+v err=%v emit=%+v\n", rec, err, txn)
		}
		for _, lsn := range confirms {
			out += fmt.Sprintf("confirm %d err=%v\n", lsn, s.Confirm(lsn))
		}
		for _, txn := range s.Recover() {
			out += fmt.Sprintf("recover emit=%+v\n", txn)
		}
		restart, confirmed := s.Positions()
		out += fmt.Sprintf("restart=%d confirmed=%d retainedFrom=%d\n", restart, confirmed, s.RetainedFrom())
		return out
	}

	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); got != first {
			t.Fatalf("run %d differs:\nfirst:\n%s\ngot:\n%s", i, first, got)
		}
	}
	t.Logf("6 identical runs, deterministic output:\n%s", first)
}
