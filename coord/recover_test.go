package coord_test

import (
	"errors"
	"testing"

	"ontology/coord"
	"ontology/ledger"
	"ontology/tcc"
)

// checkInvariants 校验账目不变量：fz 合计、Abort 不留 Tried、Commit 每分支终态。
func checkInvariants(t *testing.T, tm *tcc.Manager, c *coord.Coordinator, xid string, bs []coord.BranchSpec) {
	t.Helper()
	var triedSum int64
	for i, b := range bs {
		rec, ok := tm.Get([]byte(xid), b.BR)
		st := c.Status([]byte(xid))
		if st.Decision == coord.DecisionAbort {
			if ok && rec.State == tcc.StateTried {
				t.Fatalf("崩溃点#%d: Abort 事务残留 Tried %s", i, b.BR)
			}
			if ok && rec.State != tcc.StateCancelled {
				t.Fatalf("崩溃点#%d: Abort 分支 %s 未取消: %+v", i, b.BR, rec)
			}
		}
		if st.Decision == coord.DecisionCommit {
			broken := map[string]bool{}
			for _, s := range st.Broken {
				broken[s] = true
			}
			if !(recOkConfirmed(rec, ok) || broken[string(b.BR)]) {
				t.Fatalf("崩溃点#%d: Commit 分支 %s 既非 Confirmed 也非 Broken: %+v",
					i, b.BR, rec)
			}
		}
		if ok && rec.State == tcc.StateTried {
			triedSum += rec.Amount
		}
	}
	if got := tm.Ledger().FrozenTotal(); got != triedSum {
		t.Fatalf("fz 合计=%d != Tried 金额合计=%d", got, triedSum)
	}
}

func recOkConfirmed(rec tcc.Branch, ok bool) bool {
	return ok && rec.State == tcc.StateConfirmed
}

// TestCrashPoints 遍历 Run 中每个 Try 之后与决议写入之后的崩溃点，Recover 后校验不变量。
func TestCrashPoints(t *testing.T) {
	// Commit 路径（资源充足）：在每个 try 点与 decision 点崩溃。
	for _, cp := range []struct {
		kind string
		step int
	}{
		{"try", 0}, {"try", 1}, {"decision", 1},
	} {
		c, tm := setup(100, 100, 1000)
		bs := branches()
		_ = c.Begin([]byte("x"), bs, 0)
		c.SetCrashHook(func(p coord.CrashPoint) bool {
			return p.Kind == cp.kind && p.Step == cp.step
		})
		func() {
			defer func() { _ = recover() }()
			_, _ = c.Run([]byte("x"), 1)
		}()
		c.SetCrashHook(nil)
		d, _, _, err := c.Recover([]byte("x"), 2)
		if err != nil {
			t.Fatalf("cp=%s/%d Recover: %v", cp.kind, cp.step, err)
		}
		if cp.kind == "decision" && d != coord.DecisionCommit {
			t.Fatalf("决策点崩溃应保持 Commit, got %v", d)
		}
		if cp.kind == "try" && d != coord.DecisionAbort {
			t.Fatalf("决议前崩溃恢复应补 Abort, got %v", d)
		}
		checkInvariants(t, tm, c, "x", bs)
		t.Logf("崩溃点 %s/%d：Recover 决议=%v，fz=%d 不变量通过",
			cp.kind, cp.step, d, tm.Ledger().FrozenTotal())
	}
	// Abort 路径（b 不足）：崩溃发生在 br1 Try 之后、决议之前 -> 恢复 Abort 取消 br1、空回滚 br2。
	c, tm := setup(100, 30, 1000)
	bs := branches()
	_ = c.Begin([]byte("x"), bs, 0)
	c.SetCrashHook(func(p coord.CrashPoint) bool { return p.Kind == "try" && p.Step == 0 })
	func() { defer func() { _ = recover() }(); _, _ = c.Run([]byte("x"), 1) }()
	c.SetCrashHook(nil)
	d, _, _, err := c.Recover([]byte("x"), 2)
	if err != nil || d != coord.DecisionAbort {
		t.Fatalf("abort 恢复 d=%v err=%v", d, err)
	}
	checkInvariants(t, tm, c, "x", bs)
	if err := tm.Try([]byte("x"), []byte("2"), []byte("b"), 1, 3); !errors.Is(err, ledger.ErrHanging) {
		t.Fatalf("恢复后迟到 Try 应 ErrHanging, got %v", err)
	}
	t.Logf("Abort 路径崩溃恢复：br1 取消、br2 空回滚，迟到 Try=%v 已拦截", ledger.ErrHanging)
}

// TestReplayDeterministic：相同操作序列两次重放，决议、账目、状态完全一致。
func TestReplayDeterministic(t *testing.T) {
	run := func() (coord.Decision, int64, int64, coord.Status) {
		c, tm := setup(100, 30, 10)
		_ = c.Begin([]byte("x"), branches(), 0)
		d, _ := c.Run([]byte("x"), 0)
		_, _, _ = c.Execute([]byte("x"), 1)
		_, _, _, _ = c.Recover([]byte("x"), 2)
		return d, tm.Ledger().Balance([]byte("a")), tm.Ledger().Balance([]byte("b")),
			c.Status([]byte("x"))
	}
	d1, a1, b1, s1 := run()
	d2, a2, b2, s2 := run()
	if d1 != d2 || a1 != a2 || b1 != b2 || s1.Decision != s2.Decision ||
		len(s1.Broken) != len(s2.Broken) {
		t.Fatalf("重放不一致: (%v,%d,%d,%+v) vs (%v,%d,%d,%+v)",
			d1, a1, b1, s1, d2, a2, b2, s2)
	}
	t.Logf("两次重放一致：决议=%v bal=(%d,%d)", d1, a1, b1)
}
