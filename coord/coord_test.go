package coord_test

import (
	"errors"
	"testing"

	"ontology/coord"
	"ontology/ledger"
	"ontology/tcc"
)

func setup(aBal, bBal int64, ttl int64) (*coord.Coordinator, *tcc.Manager) {
	lg := ledger.New()
	_ = lg.Deposit([]byte("a"), aBal)
	_ = lg.Deposit([]byte("b"), bBal)
	tm, err := tcc.New(lg, ttl, 1000)
	if err != nil {
		panic(err)
	}
	return coord.New(tm), tm
}

func branches() []coord.BranchSpec {
	return []coord.BranchSpec{
		{BR: []byte("1"), Acct: []byte("a"), Amount: 60},
		{BR: []byte("2"), Acct: []byte("b"), Amount: 50},
	}
}

// 协调例：a=100,b=30，br2 不足 -> Abort；Execute 取消 br1、空回滚 br2；
// 之后迟到的 Try(br2) 必须 ErrHanging。
func TestCoordAbortExample(t *testing.T) {
	c, tm := setup(100, 30, 10)
	if err := c.Begin([]byte("x"), branches(), 0); err != nil {
		t.Fatal(err)
	}
	d, err := c.Run([]byte("x"), 0)
	t.Logf("输入 Run(x,0) -> 决议=%v err=%v；判定 br1 Try 成功、br2 ErrInsufficient，写 Abort", d, err)
	if err != nil || d != coord.DecisionAbort {
		t.Fatalf("decision=%v err=%v", d, err)
	}
	broken, berrs, err := c.Execute([]byte("x"), 1)
	t.Logf("输入 Execute(x,1) -> broken=%v errs=%v err=%v；br1 取消、br2 空回滚", broken, berrs, err)
	if err != nil || len(berrs) != 0 {
		t.Fatalf("execute err=%v berrs=%v", err, berrs)
	}
	a := tm.Ledger()
	if a.Frozen([]byte("a")) != 0 || a.Frozen([]byte("b")) != 0 {
		t.Fatalf("fz a=%d b=%d 应全部释放", a.Frozen([]byte("a")), a.Frozen([]byte("b")))
	}
	if a.Balance([]byte("a")) != 100 || a.Balance([]byte("b")) != 30 {
		t.Fatalf("Abort 后余额不得变化")
	}
	err = tm.Try([]byte("x"), []byte("2"), []byte("b"), 1, 2)
	t.Logf("迟到 Try(x,2,b,1,2) -> %v；判定空回滚标记拦截 ErrHanging", err)
	if !errors.Is(err, ledger.ErrHanging) {
		t.Fatalf("err=%v want ErrHanging", err)
	}
	// 重复 Run 返回同一决议。
	d2, _ := c.Run([]byte("x"), 3)
	if d2 != coord.DecisionAbort {
		t.Fatalf("重复 Run 决议被改变: %v", d2)
	}
}

func TestCoordCommitOK(t *testing.T) {
	c, tm := setup(100, 100, 10)
	_ = c.Begin([]byte("x"), branches(), 0)
	d, _ := c.Run([]byte("x"), 0)
	if d != coord.DecisionCommit {
		t.Fatalf("want Commit got %v", d)
	}
	if _, _, err := c.Execute([]byte("x"), 5); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if tm.Ledger().Balance([]byte("a")) != 40 || tm.Ledger().Balance([]byte("b")) != 50 {
		t.Fatalf("Commit 后 bal a=%d b=%d want 40/50",
			tm.Ledger().Balance([]byte("a")), tm.Ledger().Balance([]byte("b")))
	}
	st := c.Status([]byte("x"))
	if st.NeedsManual || len(st.Broken) != 0 {
		t.Fatalf("正常 Commit 不应 NeedsManual: %+v", st)
	}
	// 再次 Execute 幂等。
	_, _, err := c.Execute([]byte("x"), 6)
	if err != nil {
		t.Fatalf("重复 Execute: %v", err)
	}
}

// Commit 决议后某分支在 Execute 前到期 -> Broken + NeedsManual，不自动重 Try。
func TestCommitExpiredBroken(t *testing.T) {
	c, tm := setup(100, 100, 10)
	_ = c.Begin([]byte("x"), branches(), 0)
	if d, _ := c.Run([]byte("x"), 0); d != coord.DecisionCommit {
		t.Fatal("want Commit")
	}
	broken, _, err := c.Execute([]byte("x"), 10) // 恰在到期点
	t.Logf("Commit 后 Execute(x,10) -> broken=%v err=%v", broken, err)
	if !errors.Is(err, coord.ErrNeedsManual) || len(broken) != 2 {
		t.Fatalf("broken=%v err=%v want 2 branches NeedsManual", broken, err)
	}
	st := c.Status([]byte("x"))
	if !st.NeedsManual {
		t.Fatal("Status 应反映 NeedsManual")
	}
	// 被拒 Confirm 不落实到期：分支仍 Tried、冻结保留；系统不自动重新 Try。
	if tm.Ledger().FrozenTotal() != 110 {
		t.Fatalf("到期未被其它成功操作落实前冻结应保留 fz=%d want 110", tm.Ledger().FrozenTotal())
	}
	t.Logf("Broken 分支冻结保留 fz=%d，等待人工处理，无自动重新预留", tm.Ledger().FrozenTotal())
}
