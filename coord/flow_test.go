package coord

import (
	"errors"
	"testing"

	"ontology/tcc"
)

// 题目协调例：a=100,b=30；1=(a,60) 成功，2=(b,50) 不足 -> Abort。
func TestCoordinatorAbortExample(t *testing.T) {
	co, rm, lg, _ := setup(t, 10, 100, map[string]int64{"a": 100, "b": 30})
	brs := []BranchSpec{{BR: "1", Acct: "a", Amount: 60}, {BR: "2", Acct: "b", Amount: 50}}
	if err := co.Begin("x", brs, 0); err != nil {
		t.Fatal(err)
	}
	d, err := co.Run("x", 1)
	if err != nil || d != Abort {
		t.Fatalf("Run d=%v err=%v want Abort", d, err)
	}
	d2, err := co.Run("x", 2)
	if err != nil || d2 != Abort {
		t.Fatalf("rerun d=%v err=%v", d2, err)
	}
	res, err := co.Execute("x", 3)
	if err != nil || len(res.Errors) != 0 || res.Decision != Abort {
		t.Fatalf("Execute res=%+v err=%v", res, err)
	}
	if lg.Bal("a") != 100 || lg.Fz("a") != 0 || lg.Bal("b") != 30 || lg.Fz("b") != 0 {
		t.Fatalf("ledger not restored: a=%d/%d b=%d/%d", lg.Bal("a"), lg.Fz("a"), lg.Bal("b"), lg.Fz("b"))
	}
	if err := rm.Try("x", "2", "b", 50, 4); !errors.Is(err, tcc.ErrHanging) {
		t.Fatalf("late Try err=%v want ErrHanging", err)
	}
	if err := rm.Try("x", "1", "a", 1, 4); !errors.Is(err, tcc.ErrHanging) {
		t.Fatalf("late Try on cancelled branch err=%v", err)
	}
	for _, b := range brs {
		if rb, ok := rm.Get("x", b.BR); !ok || rb.State != tcc.StateCancelled {
			t.Fatalf("branch %s not cancelled: %+v %v", b.BR, rb, ok)
		}
	}
}

func TestCoordinatorCommit(t *testing.T) {
	co, _, lg, _ := setup(t, 100, 100, map[string]int64{"a": 100, "b": 100})
	brs := []BranchSpec{{BR: "1", Acct: "a", Amount: 60}, {BR: "2", Acct: "b", Amount: 40}}
	if err := co.Begin("x", brs, 0); err != nil {
		t.Fatal(err)
	}
	d, err := co.Run("x", 1)
	if err != nil || d != Commit {
		t.Fatalf("d=%v err=%v", d, err)
	}
	res, err := co.Execute("x", 2)
	if err != nil || len(res.Errors) != 0 || res.NeedsManual {
		t.Fatalf("Execute res=%+v err=%v", res, err)
	}
	if lg.Bal("a") != 40 || lg.Bal("b") != 60 || lg.Fz("a") != 0 || lg.Fz("b") != 0 {
		t.Fatalf("a=%d fz=%d b=%d fz=%d", lg.Bal("a"), lg.Fz("a"), lg.Bal("b"), lg.Fz("b"))
	}
	for _, b := range brs {
		if rb, _ := co.rm.Get("x", b.BR); rb.State != tcc.StateConfirmed {
			t.Fatalf("branch %s state=%v", b.BR, rb.State)
		}
	}
	res2, err := co.Execute("x", 3)
	if err != nil || len(res2.Errors) != 0 {
		t.Fatalf("re-execute: %+v %v", res2, err)
	}
}

// Commit 决议下分支在 Execute 前到期：Broken + NeedsManual，不自动重 Try。
func TestCommitExpiredBroken(t *testing.T) {
	co, rm, lg, _ := setup(t, 10, 100, map[string]int64{"a": 100, "b": 100})
	brs := []BranchSpec{{BR: "1", Acct: "a", Amount: 60}, {BR: "2", Acct: "b", Amount: 40}}
	if err := co.Begin("x", brs, 0); err != nil {
		t.Fatal(err)
	}
	if d, err := co.Run("x", 0); err != nil || d != Commit {
		t.Fatalf("run d=%v err=%v", d, err)
	}
	res, err := co.Execute("x", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Broken) != 2 || !res.NeedsManual {
		t.Fatalf("broken=%v manual=%v", res.Broken, res.NeedsManual)
	}
	if lg.Fz("a") != 0 || lg.Fz("b") != 0 || lg.Bal("a") != 100 || lg.Bal("b") != 100 {
		t.Fatalf("a=%d/%d b=%d/%d", lg.Bal("a"), lg.Fz("a"), lg.Bal("b"), lg.Fz("b"))
	}
	for _, i := range res.Broken {
		if rb, _ := rm.Get("x", brs[i].BR); rb.Reason != tcc.Expired {
			t.Fatalf("branch %d reason=%v", i, rb.Reason)
		}
	}
	st, err := co.Status("x")
	if err != nil || !st.NeedsManual || len(st.Broken) != 2 || !st.Done {
		t.Fatalf("status=%+v err=%v", st, err)
	}
	res2, err := co.Execute("x", 11)
	if err != nil || len(res2.Broken) != 2 {
		t.Fatalf("re-exec broken=%v err=%v", res2.Broken, err)
	}
}

// 混合：一个分支已 Confirm，另一个到期 Broken。
func TestCommitPartialBroken(t *testing.T) {
	co, _, lg, _ := setup(t, 10, 100, map[string]int64{"a": 100, "b": 100})
	brs := []BranchSpec{{BR: "1", Acct: "a", Amount: 60}, {BR: "2", Acct: "b", Amount: 40}}
	if err := co.Begin("x", brs, 0); err != nil {
		t.Fatal(err)
	}
	if d, err := co.Run("x", 0); err != nil || d != Commit {
		t.Fatalf("d=%v err=%v", d, err)
	}
	if err := co.rm.Confirm("x", "1", 1); err != nil {
		t.Fatal(err)
	}
	res, err := co.Execute("x", 10)
	if err != nil || len(res.Errors) != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(res.Broken) != 1 || res.Broken[0] != 1 || !res.NeedsManual {
		t.Fatalf("broken=%v", res.Broken)
	}
	if lg.Bal("a") != 40 || lg.Fz("a") != 0 {
		t.Fatalf("confirmed branch a bal=%d fz=%d", lg.Bal("a"), lg.Fz("a"))
	}
	if lg.Bal("b") != 100 || lg.Fz("b") != 0 {
		t.Fatalf("expired branch b bal=%d fz=%d", lg.Bal("b"), lg.Fz("b"))
	}
}
