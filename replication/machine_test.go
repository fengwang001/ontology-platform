package replication

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func begin(lsn, xid uint64) Record { return Record{LSN: lsn, XID: xid, Kind: KindBegin} }
func data(lsn, xid uint64, p []byte) Record {
	return Record{LSN: lsn, XID: xid, Kind: KindData, Payload: p}
}
func commit(lsn, xid uint64) Record { return Record{LSN: lsn, XID: xid, Kind: KindCommit} }
func abort(lsn, xid uint64) Record  { return Record{LSN: lsn, XID: xid, Kind: KindAbort} }

func mustApply(t *testing.T, m *Machine, r Record) *Transaction {
	t.Helper()
	txn, err := m.ApplyRecord(r)
	if err != nil {
		t.Fatalf("ApplyRecord(%+v) unexpected error: %v", r, err)
	}
	return txn
}

// 提交时整体发出事务；中止则丢弃，不发出。
func TestMachineCommitEmitsWholeTxnAbortDrops(t *testing.T) {
	m := NewMachine(0)
	mustApply(t, m, begin(1, 7))
	mustApply(t, m, data(2, 7, []byte("a")))
	mustApply(t, m, begin(3, 8)) // 交错事务
	mustApply(t, m, data(4, 7, []byte("b")))
	if got := m.InProgress(); got != 2 {
		t.Fatalf("InProgress = %d, want 2", got)
	}

	txn := mustApply(t, m, commit(5, 7))
	if txn == nil || txn.XID != 7 || txn.CommitLSN != 5 || len(txn.Records) != 4 {
		t.Fatalf("emitted txn = %+v", txn)
	}
	if string(txn.Records[2].Payload) != "b" {
		t.Fatalf("emitted txn payload order wrong: %+v", txn.Records)
	}

	mustApply(t, m, abort(6, 8))
	if got := m.InProgress(); got != 0 {
		t.Fatalf("InProgress after abort = %d, want 0", got)
	}
	pending := m.PendingTransactions()
	if len(pending) != 1 || pending[0].XID != 7 {
		t.Fatalf("pending = %+v, want only committed txn 7", pending)
	}
}

// 重启位点 = min(确认位点, 保留事务起点)；确认推进后日志回收。
func TestMachineRestartLSNAndReclamation(t *testing.T) {
	m := NewMachine(0)
	mustApply(t, m, begin(1, 1))
	mustApply(t, m, data(2, 1, nil))
	mustApply(t, m, commit(3, 1)) // 已提交未确认，起点 1
	mustApply(t, m, begin(4, 2))  // 进行中，起点 4
	if got := m.RestartLSN(); got != 0 {
		t.Fatalf("restart = %d, want 0 (nothing confirmed yet)", got)
	}
	if len(m.retainedLog()) != 4 {
		t.Fatalf("retained log = %d records, want 4", len(m.retainedLog()))
	}

	if err := m.Confirm(3); err != nil {
		t.Fatalf("Confirm(3): %v", err)
	}
	if m.ConfirmedLSN() != 3 || m.RestartLSN() != 3 {
		t.Fatalf("positions = (%d,%d), want restart=3 confirmed=3", m.RestartLSN(), m.ConfirmedLSN())
	}
	for _, r := range m.retainedLog() {
		if r.LSN < m.RestartLSN() {
			t.Fatalf("record %d older than restart %d not reclaimed", r.LSN, m.RestartLSN())
		}
	}

	mustApply(t, m, commit(5, 2))
	if err := m.Confirm(5); err != nil {
		t.Fatalf("Confirm(5): %v", err)
	}
	if m.RestartLSN() != 5 || m.ConfirmedLSN() != 5 {
		t.Fatalf("positions = (%d,%d), want (5,5)", m.RestartLSN(), m.ConfirmedLSN())
	}
	if len(m.retainedLog()) != 1 || m.retainedLog()[0].LSN != 5 {
		t.Fatalf("retained log = %+v, want boundary commit@5", m.retainedLog())
	}
}

// 确认必须恰好落在已发出事务的提交位点；回退被拒；同值幂等。
func TestMachineConfirmRules(t *testing.T) {
	m := NewMachine(0)
	mustApply(t, m, begin(1, 1))
	mustApply(t, m, data(2, 1, nil))
	mustApply(t, m, commit(3, 1))

	if err := m.Confirm(2); !errors.Is(err, ErrInvalidConfirm) {
		t.Fatalf("Confirm(data LSN) err = %v, want ErrInvalidConfirm", err)
	}
	if err := m.Confirm(4); !errors.Is(err, ErrInvalidConfirm) {
		t.Fatalf("Confirm(unknown) err = %v, want ErrInvalidConfirm", err)
	}
	if err := m.Confirm(3); err != nil {
		t.Fatalf("Confirm(3): %v", err)
	}
	if err := m.Confirm(2); !errors.Is(err, ErrConfirmRewound) {
		t.Fatalf("Confirm(rewind) err = %v, want ErrConfirmRewound", err)
	}
	if err := m.Confirm(3); err != nil {
		t.Fatalf("idempotent Confirm(3): %v", err)
	}
}

// 各类非法日志记录，且拒绝原因可区分、状态不变。
func TestMachineInvalidRecords(t *testing.T) {
	good := []Record{begin(1, 1), data(2, 1, nil)}
	cases := []struct {
		name string
		r    Record
		want error
	}{
		{"unknown kind", Record{LSN: 3, XID: 1, Kind: Kind(99)}, ErrInvalidRecord},
		{"zero xid", Record{LSN: 3, XID: 0, Kind: KindData}, ErrInvalidRecord},
		{"lsn not increasing", data(2, 1, nil), ErrInvalidRecord},
		{"data without begin", data(3, 9, nil), ErrInvalidRecord},
		{"commit without begin", commit(3, 9), ErrInvalidRecord},
		{"abort without begin", abort(3, 9), ErrInvalidRecord},
		{"duplicate begin", begin(3, 1), ErrInvalidRecord},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMachine(0)
			for _, r := range good {
				mustApply(t, m, r)
			}
			snapshot := snapshotMachine(m)
			_, err := m.ApplyRecord(tc.r)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if got := snapshotMachine(m); !reflect.DeepEqual(got, snapshot) {
				t.Fatalf("state changed after rejection:\nbefore=%+v\nafter =%+v", snapshot, got)
			}
		})
	}
}

// 进行中事务超限被拒，错误类型可区分，状态不变。
func TestMachineTooManyInProgress(t *testing.T) {
	m := NewMachine(1)
	mustApply(t, m, begin(1, 1))
	before := snapshotMachine(m)
	_, err := m.ApplyRecord(begin(2, 2))
	if !errors.Is(err, ErrTooManyInProgress) {
		t.Fatalf("err = %v, want ErrTooManyInProgress", err)
	}
	if got := snapshotMachine(m); !reflect.DeepEqual(got, before) {
		t.Fatalf("state changed after rejected begin: %+v", got)
	}
	// 已有事务结束后可以再开始。
	mustApply(t, m, abort(2, 1))
	mustApply(t, m, begin(3, 2))
}

// 相同输入序列反复重放，输出完全一致；已确认事务不重发，中止事务不重发。
func TestReplayDeterministicAndReemitRules(t *testing.T) {
	records := []Record{
		begin(1, 1), data(2, 1, []byte("x")), commit(3, 1),
		begin(4, 2), data(5, 2, []byte("y")), abort(6, 2),
		begin(7, 3), data(8, 3, []byte("z")), commit(9, 3),
	}

	first, m1, err := Replay(records, 3, 0)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	second, m2, err := Replay(records, 3, 0)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay not deterministic:\n%+v\n%+v", first, second)
	}

	if len(first) != 1 || first[0].XID != 3 || first[0].CommitLSN != 9 {
		t.Fatalf("reemitted = %+v, want only committed-unconfirmed txn 3", first)
	}
	if m1.ConfirmedLSN() != 3 || m1.RestartLSN() != 1 {
		t.Fatalf("positions = (%d,%d), want restart=1 confirmed=3", m1.RestartLSN(), m1.ConfirmedLSN())
	}
	if !reflect.DeepEqual(observable(m1), observable(m2)) {
		t.Fatalf("machine observables differ across replays")
	}

	// 无确认位点时：所有已提交事务按提交 LSN 升序重发，中止的不发。
	all, _, err := Replay(records, 0, 0)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(all) != 2 || all[0].XID != 1 || all[1].XID != 3 {
		t.Fatalf("reemitted all = %+v", all)
	}
}

type machineSnapshot struct {
	next, confirmed, restart uint64
	inProgress               int
	logLen                   int
	pending                  []Transaction
}

func snapshotMachine(m *Machine) machineSnapshot {
	return machineSnapshot{
		m.NextLSN(), m.ConfirmedLSN(), m.RestartLSN(),
		m.InProgress(), len(m.retainedLog()), m.PendingTransactions(),
	}
}

func observable(m *Machine) string {
	return fmt.Sprintf("%d/%d/%d/%v", m.RestartLSN(), m.ConfirmedLSN(), m.NextLSN(), m.PendingTransactions())
}
