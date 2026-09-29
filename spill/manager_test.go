package spill

import (
	"errors"
	"fmt"
	"testing"
)

func rows(prefix string, n int) []Row {
	r := make([]Row, n)
	for i := 0; i < n; i++ {
		r[i] = Row{Data: fmt.Sprintf("%s-%d", prefix, i)}
	}
	return r
}

func strRows(ss []string) []Row {
	r := make([]Row, len(ss))
	for i, s := range ss {
		r[i] = Row{Data: s}
	}
	return r
}

func dataOf(rs []Row) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Data
	}
	return out
}

func equalStrings(a, b []string) bool {
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

func blocksEqual(a, b BlocksView) bool {
	if len(a) != len(b) {
		return false
	}
	for id, ra := range a {
		rb, ok := b[id]
		if !ok || !equalStrings(dataOf(ra), dataOf(rb)) {
			return false
		}
	}
	return true
}

func asReason(t *testing.T, err error) Reason {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("expected *spill.Error, got %v", err)
	}
	return e.Reason
}

func mustBegin(t *testing.T, m *Manager, id uint64) {
	t.Helper()
	if err := m.Begin(id); err != nil {
		t.Fatal(err)
	}
}

func mustAppend(t *testing.T, m *Manager, id uint64, ss []string) {
	t.Helper()
	if _, _, err := m.Append(id, strRows(ss)); err != nil {
		t.Fatalf("append txn %d: %v", id, err)
	}
}

func TestInvalidConfigAndArgs(t *testing.T) {
	if _, err := NewManager(Config{MemRowLimit: 0, BlockLimit: 1}, nil); asReason(t, err) != ReasonInvalidArgument {
		t.Fatal("MemRowLimit=0 must be invalid")
	}
	if _, err := NewManager(Config{MemRowLimit: 1, BlockLimit: 0}, nil); asReason(t, err) != ReasonInvalidArgument {
		t.Fatal("BlockLimit=0 must be invalid")
	}

	m, err := NewManager(Config{MemRowLimit: 2, BlockLimit: 2}, NewEventLog())
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Begin(0); asReason(t, err) != ReasonInvalidArgument {
		t.Fatal("Begin(0) must be invalid argument")
	}
	if err := m.Begin(7); err != nil {
		t.Fatal(err)
	}
	if err := m.Begin(7); asReason(t, err) != ReasonTxnDuplicate {
		t.Fatal("duplicate Begin must be TxnDuplicate")
	}
	if _, _, err := m.Append(0, rows("x", 1)); asReason(t, err) != ReasonInvalidArgument {
		t.Fatal("Append with id 0 must be invalid argument")
	}
	if _, _, err := m.Append(99, rows("x", 1)); asReason(t, err) != ReasonTxnNotFound {
		t.Fatal("Append to unknown txn must be TxnNotFound")
	}
	if _, _, err := m.Append(7, nil); asReason(t, err) != ReasonInvalidArgument {
		t.Fatal("empty append must be invalid argument")
	}
	if _, _, err := m.Append(7, []Row{{Data: ""}}); asReason(t, err) != ReasonInvalidArgument {
		t.Fatal("empty row data must be invalid argument")
	}
	if _, err := m.Commit(99); asReason(t, err) != ReasonTxnNotFound {
		t.Fatal("Commit unknown txn must be TxnNotFound")
	}
	if err := m.Rollback(99); asReason(t, err) != ReasonTxnNotFound {
		t.Fatal("Rollback unknown txn must be TxnNotFound")
	}
}

func TestSpillVictimMaxRowsTieMinID(t *testing.T) {
	ev := NewEventLog()
	m, _ := NewManager(Config{MemRowLimit: 4, BlockLimit: 10}, ev)
	mustBegin(t, m, 1)
	mustBegin(t, m, 2)
	mustBegin(t, m, 3)

	// txn1=2, txn2=2（合计 4=上限），追加 txn3 1 行 => 总数 5 > 4；
	// 最多内存行数并列取事务号最小 => 溢写 txn1。
	mustAppend(t, m, 1, []string{"t1-0", "t1-1"})
	mustAppend(t, m, 2, []string{"t2-0", "t2-1"})
	spilled, ids, err := m.Append(3, strRows([]string{"t3-0"}))
	if err != nil || !spilled || len(ids) != 1 || ids[0] != 0 {
		t.Fatalf("expected one spill with block 0, got spilled=%v ids=%v err=%v", spilled, ids, err)
	}
	if st := m.Stats(); st.MemRows != 3 {
		t.Fatalf("mem rows after spill = %d, want 3", st.MemRows)
	}
	if got := dataOf(m.Blocks()[0]); !equalStrings(got, []string{"t1-0", "t1-1"}) {
		t.Fatalf("block 0 must hold txn1 rows, got %v", got)
	}

	// 非并列：txn2 追加 2 行后以 4 行成为最大者 => 溢写 txn2，块号 1（不复用）。
	_, ids, err = m.Append(2, strRows([]string{"t2-2", "t2-3"}))
	if err != nil || len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("expected block 1 for txn2, got %v err=%v", ids, err)
	}
	if err := m.CheckInvariants(); err != nil {
		t.Fatal(err)
	}

	var spills int
	for _, e := range ev.Events() {
		if e.Op == "spill" {
			spills++
			if e.Decision != "spilled" {
				t.Fatalf("bad spill decision: %+v", e)
			}
		}
	}
	if spills != 2 {
		t.Fatalf("want 2 spill events, got %d\n%s", spills, ev.String())
	}
}

func TestCommitReplayOrderAcrossBlocksAndMemory(t *testing.T) {
	m, _ := NewManager(Config{MemRowLimit: 2, BlockLimit: 10}, NewEventLog())
	mustBegin(t, m, 1)
	mustBegin(t, m, 2)

	// 用第三方事务制造交错块号，验证提交 txn1 时只回放自己的块且按块号升序。
	mustAppend(t, m, 1, []string{"a1", "a2"})
	mustAppend(t, m, 1, []string{"a3"}) // 块0: a1,a2,a3
	mustBegin(t, m, 9)
	mustAppend(t, m, 9, []string{"z", "z"}) // 块1 属于 txn9
	mustAppend(t, m, 2, []string{"b"})
	mustAppend(t, m, 1, []string{"a4"}) // 总数超限后溢写最大者

	mustAppend(t, m, 1, []string{"a5", "a6"})
	mustAppend(t, m, 1, []string{"a7"})

	out, err := m.Commit(1)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7"}
	if got := dataOf(out); !equalStrings(got, want) {
		t.Fatalf("commit replay order = %v, want %v", got, want)
	}
	if got := dataOf(m.CommittedLog()); !equalStrings(got, want) {
		t.Fatalf("committed log = %v, want %v", got, want)
	}

	// txn9 与 txn2 的块不被 txn1 提交删除。
	for _, st := range []Stats{m.Stats()} {
		if st.OpenTxnCount != 2 {
			t.Fatalf("open txn count = %d, want 2", st.OpenTxnCount)
		}
		if st.BlockCount == 0 {
			t.Fatal("other transactions' blocks must survive commit")
		}
	}
	if _, err := m.Commit(1); asReason(t, err) != ReasonTxnNotFound {
		t.Fatal("commit twice must be TxnNotFound")
	}
	if err := m.Rollback(9); err != nil {
		t.Fatal(err)
	}
	if out2, err := m.Commit(2); err != nil || !equalStrings(dataOf(out2), []string{"b"}) {
		t.Fatalf("txn2 commit = %v, err=%v", dataOf(out2), err)
	}
	if err := m.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
	if st := m.Stats(); st.NextBlock == 0 || st.BlockCount != 0 {
		t.Fatalf("final stats = %+v, block ids must not be reused", st)
	}
}

func TestRollbackDropsEverything(t *testing.T) {
	m, _ := NewManager(Config{MemRowLimit: 2, BlockLimit: 10}, NewEventLog())
	mustBegin(t, m, 1)
	mustBegin(t, m, 2)
	mustAppend(t, m, 1, []string{"a1", "a2", "a3"}) // txn1 全部进块0
	mustAppend(t, m, 2, []string{"c"})

	if err := m.Rollback(1); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Blocks()[0]; ok {
		t.Fatal("rollback must delete spill blocks")
	}
	if len(m.CommittedLog()) != 0 {
		t.Fatal("rollback must not output any rows")
	}
	if st := m.Stats(); st.MemRows != 1 {
		t.Fatalf("mem rows after rollback = %d, want 1 (txn2 untouched)", st.MemRows)
	}
	if err := m.Rollback(1); asReason(t, err) != ReasonTxnNotFound {
		t.Fatal("second rollback must be TxnNotFound")
	}
	if err := m.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestSpillStorageFullRejectsAtomically(t *testing.T) {
	ev := NewEventLog()
	m, _ := NewManager(Config{MemRowLimit: 2, BlockLimit: 1}, ev)
	mustBegin(t, m, 1)
	mustBegin(t, m, 2)
	mustBegin(t, m, 3)
	mustAppend(t, m, 1, []string{"a"})
	mustAppend(t, m, 2, []string{"b1", "b2"}) // 块0：唯一名额被占用

	snap := m.Stats()
	snapBlocks := m.Blocks()
	snapLog := m.CommittedLog()

	// 追加 txn3 两行：总数 4 > 2，还需 1 个块 => 存储满，整体拒绝。
	spilled, ids, err := m.Append(3, strRows([]string{"c1", "c2"}))
	if asReason(t, err) != ReasonSpillStorageFull {
		t.Fatalf("want SpillStorageFull, got %v", err)
	}
	if spilled || ids != nil {
		t.Fatal("rejected append must report no spill")
	}
	if after := m.Stats(); after != snap {
		t.Fatalf("state changed after rejection: before=%+v after=%+v", snap, after)
	}
	if !blocksEqual(m.Blocks(), snapBlocks) {
		t.Fatal("blocks changed after rejection")
	}
	if !equalStrings(dataOf(m.CommittedLog()), dataOf(snapLog)) {
		t.Fatal("committed log changed after rejection")
	}

	var sawReject bool
	for _, e := range ev.Events() {
		if e.Op == "append" && e.Decision == "rejected: spill storage full" {
			sawReject = true
			if e.MemRows != snap.MemRows {
				t.Fatalf("reject event memRows=%d, want %d", e.MemRows, snap.MemRows)
			}
		}
	}
	if !sawReject {
		t.Fatalf("missing rejection event\n%s", ev.String())
	}
}

func TestRejectionsDoNotChangeState(t *testing.T) {
	m, _ := NewManager(Config{MemRowLimit: 2, BlockLimit: 5}, NewEventLog())
	mustBegin(t, m, 1)
	mustAppend(t, m, 1, []string{"a1", "a2", "a3"}) // 块0

	calls := []func() error{
		func() error { _, _, e := m.Append(1, nil); return e },
		func() error { _, _, e := m.Append(44, strRows([]string{"x"})); return e },
		func() error { _, e := m.Commit(44); return e },
		func() error { return m.Rollback(44) },
		func() error { return m.Begin(1) },
	}
	for i, call := range calls {
		st0 := m.Stats()
		b0 := m.Blocks()
		log0 := m.CommittedLog()
		if err := call(); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
		if m.Stats() != st0 {
			t.Fatalf("case %d: stats changed", i)
		}
		if !blocksEqual(m.Blocks(), b0) {
			t.Fatalf("case %d: blocks changed", i)
		}
		if !equalStrings(dataOf(m.CommittedLog()), dataOf(log0)) {
			t.Fatalf("case %d: committed log changed", i)
		}
	}
}
