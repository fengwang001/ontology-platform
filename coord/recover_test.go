package coord

import (
	"errors"
	"testing"

	"ontology/ledger"
	"ontology/tcc"
)

func abortBranches() []BranchSpec {
	return []BranchSpec{
		{BR: "1", Acct: "a", Amount: 60},
		{BR: "2", Acct: "b", Amount: 50},
	}
}

// 在 Run 每个分支 Try 成功之后注入崩溃，Recover 后：
// 无决议 => Abort，已 Try 分支被取消，后续分支空回滚，账目复原。
func TestRecoverAfterRunCrashAbort(t *testing.T) {
	co, rm, lg, _ := setup(t, 100, 100, map[string]int64{"a": 100, "b": 30})
	brs := abortBranches()
	if err := co.Begin("x", brs, 0); err != nil {
		t.Fatal(err)
	}
	// 仅分支 1 能成功；分支 2 Try 失败立即停，不会到达第二个崩溃点。
	co.setCrashHook(func(xid string, i int) bool { return i == 0 })
	if _, err := co.Run("x", 1); !errors.Is(err, ErrCrashed) {
		t.Fatalf("run err=%v want ErrCrashed", err)
	}
	if lg.Fz("a") != 60 {
		t.Fatalf("fz(a)=%d want 60", lg.Fz("a"))
	}
	res, err := co.Recover("x", 5)
	if err != nil || res.Decision != Abort || len(res.Errors) != 0 {
		t.Fatalf("recover res=%+v err=%v", res, err)
	}
	assertRestored(t, lg, rm, brs)
	st, _ := co.Status("x")
	if st.Decision != Abort || st.NeedsManual {
		t.Fatalf("status=%+v", st)
	}
	if d, err := co.Run("x", 6); err != nil || d != Abort {
		t.Fatalf("rerun d=%v err=%v", d, err)
	}
}

// Commit 例覆盖两个分支后的崩溃点：Recover 无决议写 Abort，
// 已 Try 的分支全部取消。
func TestRecoverAfterRunCrashCommitCase(t *testing.T) {
	for crashAt := 0; crashAt < 2; crashAt++ {
		co, rm, lg, _ := setup(t, 100, 100, map[string]int64{"a": 100, "b": 100})
		brs := []BranchSpec{{BR: "1", Acct: "a", Amount: 60}, {BR: "2", Acct: "b", Amount: 40}}
		if err := co.Begin("x", brs, 0); err != nil {
			t.Fatal(err)
		}
		co.setCrashHook(func(xid string, i int) bool { return i == crashAt })
		if _, err := co.Run("x", 1); !errors.Is(err, ErrCrashed) {
			t.Fatalf("crashAt=%d run err=%v", crashAt, err)
		}
		if lg.Fz("a") != 60 {
			t.Fatalf("crashAt=%d fz(a)=%d", crashAt, lg.Fz("a"))
		}
		res, err := co.Recover("x", 5)
		if err != nil || res.Decision != Abort || len(res.Errors) != 0 {
			t.Fatalf("crashAt=%d recover=%+v %v", crashAt, res, err)
		}
		if lg.Bal("a") != 100 || lg.Bal("b") != 100 || lg.Fz("a") != 0 || lg.Fz("b") != 0 {
			t.Fatalf("crashAt=%d a=%d/%d b=%d/%d", crashAt,
				lg.Bal("a"), lg.Fz("a"), lg.Bal("b"), lg.Fz("b"))
		}
		for _, b := range brs {
			if rb, _ := rm.Get("x", b.BR); rb.State != tcc.StateCancelled {
				t.Fatalf("crashAt=%d branch %s = %+v", crashAt, b.BR, rb)
			}
		}
		// 迟到 Try 必被悬挂拦截。
		if err := rm.Try("x", brs[crashAt].BR, "a", 1, 7); !errors.Is(err, tcc.ErrHanging) {
			t.Fatalf("crashAt=%d late try err=%v", crashAt, err)
		}
	}
}

// 在 Execute(Abort) 每一步之后注入崩溃；再次 Recover 幂等完成。
func TestRecoverAfterExecuteCrash(t *testing.T) {
	for crashAt := 0; crashAt < 2; crashAt++ {
		co, rm, lg, _ := setup(t, 100, 100, map[string]int64{"a": 100, "b": 30})
		brs := abortBranches()
		if err := co.Begin("x", brs, 0); err != nil {
			t.Fatal(err)
		}
		if d, err := co.Run("x", 1); err != nil || d != Abort {
			t.Fatalf("d=%v err=%v", d, err)
		}
		co.setExecHook(func(xid string, i int) bool { return i == crashAt })
		_, err := co.Execute("x", 2)
		if !errors.Is(err, ErrCrashed) {
			t.Fatalf("crashAt=%d exec err=%v", crashAt, err)
		}
		co.setExecHook(nil)
		res, err := co.Recover("x", 3)
		if err != nil || res.Decision != Abort || len(res.Errors) != 0 {
			t.Fatalf("crashAt=%d recover=%+v %v", crashAt, res, err)
		}
		assertRestored(t, lg, rm, brs)
	}
}

// Commit 场景下在 Execute 第一步（Confirm）后崩溃：Recover 继续 Confirm，
// 决议不变，每分支 Confirmed，余额精确。
func TestRecoverCommitMidExecute(t *testing.T) {
	co, rm, lg, _ := setup(t, 100, 100, map[string]int64{"a": 100, "b": 100})
	brs := []BranchSpec{{BR: "1", Acct: "a", Amount: 60}, {BR: "2", Acct: "b", Amount: 40}}
	if err := co.Begin("x", brs, 0); err != nil {
		t.Fatal(err)
	}
	if d, err := co.Run("x", 1); err != nil || d != Commit {
		t.Fatalf("d=%v err=%v", d, err)
	}
	co.setExecHook(func(xid string, i int) bool { return i == 0 })
	if _, err := co.Execute("x", 2); !errors.Is(err, ErrCrashed) {
		t.Fatalf("exec err=%v", err)
	}
	co.setExecHook(nil)
	res, err := co.Recover("x", 3)
	if err != nil || res.Decision != Commit || len(res.Errors) != 0 || res.NeedsManual {
		t.Fatalf("recover=%+v %v", res, err)
	}
	if lg.Bal("a") != 40 || lg.Bal("b") != 60 {
		t.Fatalf("bal a=%d b=%d", lg.Bal("a"), lg.Bal("b"))
	}
	for _, b := range brs {
		if rb, _ := rm.Get("x", b.BR); rb.State != tcc.StateConfirmed {
			t.Fatalf("branch %s state=%v", b.BR, rb.State)
		}
	}
}

// Commit 执行中跨过到期点崩溃：已 Confirm 的保留，未 Confirm 的 Recover 后 Broken。
func TestRecoverCommitWithExpiry(t *testing.T) {
	co, rm, lg, _ := setup(t, 10, 100, map[string]int64{"a": 100, "b": 100})
	brs := []BranchSpec{{BR: "1", Acct: "a", Amount: 60}, {BR: "2", Acct: "b", Amount: 40}}
	if err := co.Begin("x", brs, 0); err != nil {
		t.Fatal(err)
	}
	if d, err := co.Run("x", 0); err != nil || d != Commit {
		t.Fatalf("d=%v err=%v", d, err)
	}
	// 在 10 执行：两分支都到期，第一步即 Broken 后崩溃。
	co.setExecHook(func(xid string, i int) bool { return i == 0 })
	if _, err := co.Execute("x", 10); !errors.Is(err, ErrCrashed) {
		t.Fatalf("exec err=%v", err)
	}
	co.setExecHook(nil)
	res, err := co.Recover("x", 11)
	if err != nil || len(res.Broken) != 2 || !res.NeedsManual {
		t.Fatalf("recover=%+v err=%v", res, err)
	}
	if lg.Bal("a") != 100 || lg.Bal("b") != 100 || lg.Fz("a") != 0 || lg.Fz("b") != 0 {
		t.Fatalf("a=%d/%d b=%d/%d", lg.Bal("a"), lg.Fz("a"), lg.Bal("b"), lg.Fz("b"))
	}
	for _, b := range brs {
		if rb, _ := rm.Get("x", b.BR); rb.State != tcc.StateCancelled || rb.Reason != tcc.Expired {
			t.Fatalf("branch %s = %+v", b.BR, rb)
		}
	}
}

func assertRestored(t *testing.T, lg *ledger.Ledger, rm *tcc.TCC, brs []BranchSpec) {
	t.Helper()
	if lg.Fz("a") != 0 || lg.Fz("b") != 0 || lg.Bal("a") != 100 || lg.Bal("b") != 30 {
		t.Fatalf("a=%d/%d b=%d/%d", lg.Bal("a"), lg.Fz("a"), lg.Bal("b"), lg.Fz("b"))
	}
	for _, b := range brs {
		rb, ok := rm.Get("x", b.BR)
		if !ok || rb.State != tcc.StateCancelled {
			t.Fatalf("branch %s = %+v ok=%v", b.BR, rb, ok)
		}
	}
}
