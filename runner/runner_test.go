package runner_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"ontology/ledger"
	"ontology/runner"
	"ontology/script"
)

// harness 包装引擎并记录回调调用次序；failAt/panicAt 以回调全局序号注入失败与 panic。
type harness struct {
	eng     *runner.Engine
	execLog []uint32
	undoLog []uint32
	callN   int
	failAt  map[int]bool
	panicAt map[int]bool
}

func newHarness(t *testing.T, ooo bool, lim int, failAt, panicAt map[int]bool) *harness {
	t.Helper()
	h := &harness{failAt: failAt, panicAt: panicAt}
	cb := func(ver uint32) error {
		h.callN++
		if h.panicAt[h.callN] {
			panic("injected panic")
		}
		if h.failAt[h.callN] {
			return fmt.Errorf("injected failure at call %d", h.callN)
		}
		return nil
	}
	eng, err := runner.New(ooo, lim,
		func(ver uint32) error { h.execLog = append(h.execLog, ver); return cb(ver) },
		func(ver uint32) error { h.undoLog = append(h.undoLog, ver); return cb(ver) })
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.eng = eng
	return h
}

func (h *harness) register(t *testing.T, ver uint32, sum uint64, hasUndo bool) {
	t.Helper()
	s := script.Script{Ver: ver, Sum: sum, HasUndo: hasUndo}
	if err := h.eng.Register(runner.RoleMigrator, s); err != nil {
		t.Fatalf("Register(ver=%d): %v", ver, err)
	}
}

func (h *harness) migrate(t *testing.T, now uint64) (applied []uint32, more bool) {
	t.Helper()
	applied, failed, more, err := h.eng.Migrate(runner.RoleMigrator, now)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if failed != 0 {
		t.Fatalf("Migrate unexpected failedVer=%d", failed)
	}
	return applied, more
}

// checkReject 断言 err 是带有指定哨兵与 ver 的 *runner.Reject。
func checkReject(t *testing.T, err error, sentinel error, wantVer uint32) {
	t.Helper()
	if err == nil {
		t.Fatalf("want reject %v(ver=%d), got nil", sentinel, wantVer)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel %v, got %v", sentinel, err)
	}
	var rj *runner.Reject
	if !errors.As(err, &rj) {
		t.Fatalf("err %v is not *runner.Reject", err)
	}
	if rj.Ver != wantVer {
		t.Fatalf("want ver=%d, got %d (%v)", wantVer, rj.Ver, err)
	}
}

func wantRows(t *testing.T, h *harness, want []ledger.Row) {
	t.Helper()
	got := h.eng.Ledger()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ledger mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

func row(rank uint64, ver uint32, sum uint64, st ledger.Status) ledger.Row {
	return ledger.Row{Rank: rank, Ver: ver, Sum: sum, Status: st}
}

// 例一（ooo=true）：乱序补执行、Undo 按 rank 降序、回退后 M 只由 S 行决定。
func TestExample1OutOfOrderAllowed(t *testing.T) {
	h := newHarness(t, true, 10, nil, nil)
	h.register(t, 1, 100, true)
	h.register(t, 3, 300, true)
	applied, more := h.migrate(t, 1)
	if !reflect.DeepEqual(applied, []uint32{1, 3}) || more {
		t.Fatalf("migrate1: applied=%v more=%v", applied, more)
	}
	h.register(t, 2, 200, true)
	applied, more = h.migrate(t, 2)
	if !reflect.DeepEqual(applied, []uint32{2}) || more {
		t.Fatalf("migrate2: applied=%v more=%v", applied, more)
	}
	wantRows(t, h, []ledger.Row{
		row(1, 1, 100, ledger.StatusSuccess),
		row(2, 3, 300, ledger.StatusSuccess),
		row(3, 2, 200, ledger.StatusSuccess),
	})
	// Undo(to=1)：选中 ver3(rank2) 与 ver2(rank3)，按 rank 降序先退 ver2。
	undone, failed, err := h.eng.Undo(runner.RoleAdmin, 3, 1)
	if err != nil || failed != 0 {
		t.Fatalf("Undo: err=%v failed=%d", err, failed)
	}
	if !reflect.DeepEqual(undone, []uint32{2, 3}) || !reflect.DeepEqual(h.undoLog, []uint32{2, 3}) {
		t.Fatalf("undo order: undone=%v undoLog=%v", undone, h.undoLog)
	}
	wantRows(t, h, []ledger.Row{
		row(1, 1, 100, ledger.StatusSuccess),
		row(2, 3, 300, ledger.StatusUndone),
		row(3, 2, 200, ledger.StatusUndone),
	})
	// A={1}，M=1：再 Migrate 按 2、3 升序执行，不算乱序。
	applied, more = h.migrate(t, 4)
	if !reflect.DeepEqual(applied, []uint32{2, 3}) || more {
		t.Fatalf("migrate3: applied=%v more=%v", applied, more)
	}
	wantRows(t, h, []ledger.Row{
		row(1, 1, 100, ledger.StatusSuccess),
		row(2, 3, 300, ledger.StatusUndone),
		row(3, 2, 200, ledger.StatusUndone),
		row(4, 2, 200, ledger.StatusSuccess),
		row(5, 3, 300, ledger.StatusSuccess),
	})
}

// 例一（ooo=false）：乱序被拒绝且账本不变。
func TestExample1OutOfOrderRejected(t *testing.T) {
	h := newHarness(t, false, 10, nil, nil)
	h.register(t, 1, 100, true)
	h.register(t, 3, 300, true)
	h.migrate(t, 1)
	h.register(t, 2, 200, true)
	before := h.eng.Ledger()
	_, _, _, err := h.eng.Migrate(runner.RoleMigrator, 2)
	checkReject(t, err, runner.ErrOutOfOrder, 2)
	if got := h.eng.Ledger(); !reflect.DeepEqual(got, before) {
		t.Fatalf("rejected migrate changed ledger: %+v", got)
	}
}

// 例一（回退中途失败）：U 保留、追加 F 行、A 与 M、Repair 后 rank 不回收。
func TestExample1UndoFailureMidway(t *testing.T) {
	// 回调序号：exec(1)=1, exec(3)=2, exec(2)=3, undo(2)=4, undo(3)=5（注入失败）。
	h := newHarness(t, true, 10, map[int]bool{5: true}, nil)
	h.register(t, 1, 100, true)
	h.register(t, 3, 300, true)
	h.migrate(t, 1)
	h.register(t, 2, 200, true)
	h.migrate(t, 2)
	undone, failed, err := h.eng.Undo(runner.RoleAdmin, 3, 1)
	if err != nil {
		t.Fatalf("Undo err: %v", err)
	}
	if !reflect.DeepEqual(undone, []uint32{2}) || failed != 3 {
		t.Fatalf("Undo: undone=%v failed=%d", undone, failed)
	}
	// ver2 的行为 U，ver3 仍为 S，追加 rank4 的 F 行；A={1,3}，M=3。
	wantRows(t, h, []ledger.Row{
		row(1, 1, 100, ledger.StatusSuccess),
		row(2, 3, 300, ledger.StatusSuccess),
		row(3, 2, 200, ledger.StatusUndone),
		row(4, 3, 300, ledger.StatusFailed),
	})
	// 失败后阻断：Migrate 报 ErrFailed(3)。
	_, _, _, err = h.eng.Migrate(runner.RoleMigrator, 4)
	checkReject(t, err, runner.ErrFailed, 3)
	// Repair 删除 F 行（rank 不回收），再 Migrate 重新执行 ver2 得 rank5。
	n, err := h.eng.Repair(runner.RoleAdmin, 5)
	if err != nil || n != 1 {
		t.Fatalf("Repair: n=%d err=%v", n, err)
	}
	applied, more := h.migrate(t, 6)
	if !reflect.DeepEqual(applied, []uint32{2}) || more {
		t.Fatalf("migrate after repair: applied=%v more=%v", applied, more)
	}
	wantRows(t, h, []ledger.Row{
		row(1, 1, 100, ledger.StatusSuccess),
		row(2, 3, 300, ledger.StatusSuccess),
		row(3, 2, 200, ledger.StatusUndone),
		row(5, 2, 200, ledger.StatusSuccess),
	})
}

// 例二：lim 截断与 more 标志；覆盖已应用脚本后报 ErrChecksum。
func TestExample2LimAndChecksum(t *testing.T) {
	h := newHarness(t, false, 2, nil, nil)
	for v := uint32(1); v <= 5; v++ {
		h.register(t, v, uint64(v)*10, true)
	}
	applied, more := h.migrate(t, 1)
	if !reflect.DeepEqual(applied, []uint32{1, 2}) || !more {
		t.Fatalf("migrate1: applied=%v more=%v", applied, more)
	}
	applied, more = h.migrate(t, 2)
	if !reflect.DeepEqual(applied, []uint32{3, 4}) || !more {
		t.Fatalf("migrate2: applied=%v more=%v", applied, more)
	}
	applied, more = h.migrate(t, 3)
	if !reflect.DeepEqual(applied, []uint32{5}) || more {
		t.Fatalf("migrate3: applied=%v more=%v", applied, more)
	}
	// V1 已执行后用不同 sum 重新登记：下一次 Migrate 在执行任何脚本前报 ErrChecksum(1)。
	h.register(t, 1, 999, true)
	callNBefore := h.callN
	_, _, _, err := h.eng.Migrate(runner.RoleMigrator, 4)
	checkReject(t, err, runner.ErrChecksum, 1)
	if h.callN != callNBefore {
		t.Fatalf("checksum reject fired callbacks: %d -> %d", callNBefore, h.callN)
	}
}

// lim 恰等于待执行数时 more 为假。
func TestLimExactPendingMoreFalse(t *testing.T) {
	h := newHarness(t, false, 3, nil, nil)
	for v := uint32(1); v <= 3; v++ {
		h.register(t, v, uint64(v), true)
	}
	applied, more := h.migrate(t, 1)
	if !reflect.DeepEqual(applied, []uint32{1, 2, 3}) || more {
		t.Fatalf("applied=%v more=%v", applied, more)
	}
}

// 校验和不等先于乱序报错。
func TestChecksumPrecedesOutOfOrder(t *testing.T) {
	h := newHarness(t, false, 10, nil, nil)
	h.register(t, 1, 100, true)
	h.register(t, 3, 300, true)
	h.migrate(t, 1)
	h.register(t, 2, 200, true) // 乱序候选
	h.register(t, 1, 999, true) // 覆盖已应用的 V1，制造校验和不等
	_, _, _, err := h.eng.Migrate(runner.RoleMigrator, 2)
	checkReject(t, err, runner.ErrChecksum, 1)
}

// Undo 的 to 恰等于某 ver 时该 ver 保留。
func TestUndoToExactVerKept(t *testing.T) {
	h := newHarness(t, false, 10, nil, nil)
	for v := uint32(1); v <= 3; v++ {
		h.register(t, v, uint64(v), true)
	}
	h.migrate(t, 1)
	undone, failed, err := h.eng.Undo(runner.RoleAdmin, 2, 2)
	if err != nil || failed != 0 {
		t.Fatalf("Undo: err=%v failed=%d", err, failed)
	}
	if !reflect.DeepEqual(undone, []uint32{3}) {
		t.Fatalf("undone=%v", undone)
	}
	wantRows(t, h, []ledger.Row{
		row(1, 1, 1, ledger.StatusSuccess),
		row(2, 2, 2, ledger.StatusSuccess),
		row(3, 3, 3, ledger.StatusUndone),
	})
}

// ErrNoUndo 报最大 ver，且先检查后动手（账本不变）。
func TestErrNoUndoReportsMaxVer(t *testing.T) {
	h := newHarness(t, false, 10, nil, nil)
	h.register(t, 1, 1, true)
	h.register(t, 2, 2, false)
	h.register(t, 3, 3, false)
	h.migrate(t, 1)
	before := h.eng.Ledger()
	_, _, err := h.eng.Undo(runner.RoleAdmin, 2, 0)
	checkReject(t, err, runner.ErrNoUndo, 3)
	if got := h.eng.Ledger(); !reflect.DeepEqual(got, before) {
		t.Fatalf("ErrNoUndo changed ledger: %+v", got)
	}
	if len(h.undoLog) != 0 {
		t.Fatalf("undo callbacks fired before check: %v", h.undoLog)
	}
}

// 选中为空也是成功，并推进最大 now。
func TestUndoEmptySelectionSucceeds(t *testing.T) {
	h := newHarness(t, false, 10, nil, nil)
	h.register(t, 1, 1, true)
	h.migrate(t, 5)
	undone, failed, err := h.eng.Undo(runner.RoleAdmin, 7, 1)
	if err != nil || failed != 0 || len(undone) != 0 {
		t.Fatalf("Undo: undone=%v failed=%d err=%v", undone, failed, err)
	}
	// 最大 now 已推进到 7：now=6 回退被拒。
	_, _, err = h.eng.Undo(runner.RoleAdmin, 6, 0)
	checkReject(t, err, runner.ErrNowRegression, 0)
}

// exec panic 视同失败：追加 F 行，正常返回。
func TestExecPanicTreatedAsFailure(t *testing.T) {
	h := newHarness(t, false, 10, nil, map[int]bool{2: true})
	h.register(t, 1, 1, true)
	h.register(t, 2, 2, true)
	applied, failed, more, err := h.eng.Migrate(runner.RoleMigrator, 1)
	if err != nil {
		t.Fatalf("Migrate err: %v", err)
	}
	if !reflect.DeepEqual(applied, []uint32{1}) || failed != 2 || more {
		t.Fatalf("applied=%v failed=%d more=%v", applied, failed, more)
	}
	wantRows(t, h, []ledger.Row{
		row(1, 1, 1, ledger.StatusSuccess),
		row(2, 2, 2, ledger.StatusFailed),
	})
}

// undoFn panic 视同失败：已改 U 的保留，追加 F 行。
func TestUndoPanicTreatedAsFailure(t *testing.T) {
	// 回调序号：exec(1)=1, exec(2)=2, undo(2)=3（注入 panic）。
	h := newHarness(t, false, 10, nil, map[int]bool{3: true})
	h.register(t, 1, 1, true)
	h.register(t, 2, 2, true)
	h.migrate(t, 1)
	undone, failed, err := h.eng.Undo(runner.RoleAdmin, 2, 0)
	if err != nil {
		t.Fatalf("Undo err: %v", err)
	}
	if len(undone) != 0 || failed != 2 {
		t.Fatalf("undone=%v failed=%d", undone, failed)
	}
	wantRows(t, h, []ledger.Row{
		row(1, 1, 1, ledger.StatusSuccess),
		row(2, 2, 2, ledger.StatusSuccess),
		row(3, 2, 2, ledger.StatusFailed),
	})
}

// 权限与参数校验：构造、Register、Migrate、Repair、Undo。
func TestPermissionAndArgumentChecks(t *testing.T) {
	if _, err := runner.New(false, 0, okCb, okCb); !errors.Is(err, runner.ErrArgument) {
		t.Fatalf("lim=0: %v", err)
	}
	if _, err := runner.New(false, 1001, okCb, okCb); !errors.Is(err, runner.ErrArgument) {
		t.Fatalf("lim=1001: %v", err)
	}
	if _, err := runner.New(false, 1, nil, okCb); !errors.Is(err, runner.ErrArgument) {
		t.Fatalf("nil exec: %v", err)
	}
	h := newHarness(t, false, 5, nil, nil)
	h.register(t, 1, 1, true)
	h.migrate(t, 10)

	checkReject(t, h.eng.Register(runner.RoleReadOnly, script.Script{Ver: 2, Sum: 2}), runner.ErrPermission, 0)
	checkReject(t, h.eng.Register(runner.RoleMigrator, script.Script{Ver: 0}), runner.ErrArgument, 0)
	checkReject(t, h.eng.Register(runner.RoleMigrator, script.Script{Ver: 1_000_001}), runner.ErrArgument, 0)
	checkReject(t, h.eng.Register(3, script.Script{Ver: 2, Sum: 2}), runner.ErrArgument, 0)

	_, _, _, err := h.eng.Migrate(runner.RoleReadOnly, 10)
	checkReject(t, err, runner.ErrPermission, 0)
	_, _, _, err = h.eng.Migrate(9, 10)
	checkReject(t, err, runner.ErrArgument, 0)
	_, _, _, err = h.eng.Migrate(runner.RoleMigrator, runner.MaxNow+1)
	checkReject(t, err, runner.ErrArgument, 0)
	_, _, _, err = h.eng.Migrate(runner.RoleMigrator, 9)
	checkReject(t, err, runner.ErrNowRegression, 0)

	if _, err = h.eng.Repair(runner.RoleMigrator, 10); !errors.Is(err, runner.ErrPermission) {
		t.Fatalf("Repair role=1: %v", err)
	}
	if _, err = h.eng.Repair(runner.RoleAdmin, 10); !errors.Is(err, runner.ErrNoFailed) {
		t.Fatalf("Repair no failed: %v", err)
	}
	if _, err = h.eng.Repair(runner.RoleAdmin, 9); !errors.Is(err, runner.ErrNowRegression) {
		t.Fatalf("Repair now regression: %v", err)
	}

	_, _, err = h.eng.Undo(runner.RoleMigrator, 10, 0)
	checkReject(t, err, runner.ErrPermission, 0)
	_, _, err = h.eng.Undo(runner.RoleAdmin, 10, 1_000_001)
	checkReject(t, err, runner.ErrArgument, 0)
	_, _, err = h.eng.Undo(runner.RoleAdmin, 9, 0)
	checkReject(t, err, runner.ErrNowRegression, 0)
}

func okCb(ver uint32) error { return nil }

// 被拒绝的操作不得改变账本、登记表与最大 now。
func TestRejectedOpsNoStateChange(t *testing.T) {
	h := newHarness(t, false, 5, nil, nil)
	h.register(t, 1, 1, true)
	h.register(t, 2, 2, true)
	h.migrate(t, 10)
	beforeRows := h.eng.Ledger()
	beforeReg := h.eng.Registered()

	rejects := []func() error{
		func() error { return h.eng.Register(runner.RoleReadOnly, script.Script{Ver: 9, Sum: 9}) },
		func() error { return h.eng.Register(runner.RoleMigrator, script.Script{Ver: 0}) },
		func() error { _, _, _, e := h.eng.Migrate(runner.RoleReadOnly, 10); return e },
		func() error { _, _, _, e := h.eng.Migrate(runner.RoleMigrator, 9); return e },
		func() error { _, _, _, e := h.eng.Migrate(runner.RoleMigrator, runner.MaxNow+1); return e },
		func() error { _, e := h.eng.Repair(runner.RoleMigrator, 10); return e },
		func() error { _, e := h.eng.Repair(runner.RoleAdmin, 10); return e },
		func() error { _, _, e := h.eng.Undo(runner.RoleMigrator, 10, 0); return e },
		func() error { _, _, e := h.eng.Undo(runner.RoleAdmin, 9, 0); return e },
	}
	for i, fn := range rejects {
		if err := fn(); err == nil {
			t.Fatalf("reject %d: want error", i)
		}
		if got := h.eng.Ledger(); !reflect.DeepEqual(got, beforeRows) {
			t.Fatalf("reject %d changed ledger: %+v", i, got)
		}
		if got := h.eng.Registered(); !reflect.DeepEqual(got, beforeReg) {
			t.Fatalf("reject %d changed registry: %+v", i, got)
		}
	}
	// 最大 now 未被拒绝操作推进：now=10 仍被接受。
	if _, _, _, err := h.eng.Migrate(runner.RoleMigrator, 10); err != nil {
		t.Fatalf("maxNow advanced by rejected op: %v", err)
	}
}

// Ledger 与 Registered 返回深拷贝。
func TestQueriesReturnDeepCopy(t *testing.T) {
	h := newHarness(t, false, 5, nil, nil)
	h.register(t, 1, 1, true)
	h.migrate(t, 1)
	rows := h.eng.Ledger()
	rows[0].Status = ledger.StatusFailed
	rows[0].Sum = 0
	reg := h.eng.Registered()
	reg[0].Sum = 0
	if got := h.eng.Ledger(); got[0].Status != ledger.StatusSuccess || got[0].Sum != 1 {
		t.Fatalf("ledger mutated through copy: %+v", got)
	}
	if got := h.eng.Registered(); got[0].Sum != 1 {
		t.Fatalf("registry mutated through copy: %+v", got)
	}
}
