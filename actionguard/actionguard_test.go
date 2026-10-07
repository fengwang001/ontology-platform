package actionguard

import (
	"context"
	"errors"
	"testing"
)

func newBankStore() (*Store, *Executor) {
	st := NewStore()
	ex := NewExecutor(st)
	ex.MustRegister(NewTransferAction())
	ex.MustRegister(NewWithdrawFeeAction(10))
	return st, ex
}

func mustState(t *testing.T, st *Store, id string) (string, int64, []HistoryRecord) {
	t.Helper()
	attrs, ver, _, ok := st.ObjectState(id)
	if !ok {
		t.Fatalf("object %s missing", id)
	}
	return attrs["balance"], ver, st.ObjectHistory(id)
}

// 路径1：前置通过 + 后置失败（手续费导致余额为负）。
// 状态不变性：余额、版本号、对象历史必须与从未尝试完全一致；
// 独立失败轨迹恰好记录该次失败；审计轨迹含前置通过、后置失败的依据。
func TestPrePassPostFail_StateInvariant(t *testing.T) {
	st, ex := newBankStore()
	st.CreateObject("a", map[string]string{"balance": "100"})

	balBefore, verBefore, histBefore := mustState(t, st, "a")
	acceptedBefore := len(st.AcceptedCalls())

	out := ex.Execute(context.Background(), "withdraw_fee", "call-1",
		map[string]any{"from": "a", "amount": "95"}, []string{"a"})

	if out.Class != OutcomePostRejected || out.FailedPost != "non_negative_after_fee" {
		t.Fatalf("want decisive post failure non_negative_after_fee, got %+v", out)
	}
	balAfter, verAfter, histAfter := mustState(t, st, "a")
	if balAfter != balBefore || verAfter != verBefore || verAfter != 0 {
		t.Fatalf("state changed after post failure: bal %s->%s ver %d->%d",
			balBefore, balAfter, verBefore, verAfter)
	}
	if len(histAfter) != len(histBefore) || len(st.AcceptedCalls()) != acceptedBefore {
		t.Fatalf("history polluted by post failure")
	}
	ft := st.FailureTrail()
	if len(ft) != 1 || ft[0].CallID != "call-1" || ft[0].Condition != "non_negative_after_fee" {
		t.Fatalf("failure trail mismatch: %+v", ft)
	}
	// 失败轨迹不得反映为对象状态：Store 中不存在失败轨迹对象。
	if _, _, _, ok := st.ObjectState("__failure_trail__"); ok {
		t.Fatalf("failure trail must not be materialized as object state")
	}
	audit := st.AuditTrail()
	var sawPrePass, sawPostFail bool
	for _, e := range audit {
		if e.Phase == "pre" && e.Condition == "sufficient_for_principal" && e.Result == "pass" {
			sawPrePass = true
		}
		if e.Phase == "post" && e.Condition == "non_negative_after_fee" && e.Result == "fail" {
			sawPostFail = true
			if e.Basis == "" || e.Input == "" {
				t.Fatalf("post audit missing basis/input: %+v", e)
			}
		}
	}
	if !sawPrePass || !sawPostFail {
		t.Fatalf("audit trail missing pre-pass/post-fail evidence: %+v", audit)
	}
}

// 路径2：前置失败（多个前置条件同时不通过时必须全部返回）。
// 不消耗版本号、不进入对象/动作历史、不写失败轨迹。
func TestPreFail_AllFailuresAndStateInvariant(t *testing.T) {
	st := NewStore()
	ex := NewExecutor(st)
	ex.MustRegister(NewTransferAction())
	st.CreateObject("a", map[string]string{"balance": "5"})
	// b 不存在；a 余额不足。from_active 与 to_active：a 存在、b 不存在。

	verA := int64(0)
	out := ex.Execute(context.Background(), "transfer", "call-x",
		map[string]any{"from": "a", "to": "b", "amount": "100", "total": "5"},
		[]string{"a", "b"})

	if out.Class != OutcomePreRejected {
		t.Fatalf("want pre rejection, got %+v", out)
	}
	want := map[string]bool{"to_active": true, "sufficient_funds": true}
	if len(out.FailedPre) != len(want) {
		t.Fatalf("want %d failed preconditions, got %v", len(want), out.FailedPre)
	}
	for _, name := range out.FailedPre {
		if !want[name] {
			t.Fatalf("unexpected failed precondition %q", name)
		}
	}
	if _, ver, _, ok := st.ObjectState("a"); !ok || ver != verA {
		t.Fatalf("pre failure consumed version on a")
	}
	if len(st.ObjectHistory("a")) != 0 || len(st.AcceptedCalls()) != 0 {
		t.Fatalf("pre failure recorded in history")
	}
	if len(st.FailureTrail()) != 0 {
		t.Fatalf("pre failure must not be written to post failure trail")
	}
}

// 路径3：声明自相矛盾 —— 注册期（最早阶段）即失败，动作不可执行；
// 任何执行尝试都到不了前置/后置阶段。
func TestContradictoryDefinition_RejectedAtRegistration(t *testing.T) {
	st := NewStore()
	ex := NewExecutor(st)
	err := ex.Register(NewContradictoryTransfer())
	var de *DefinitionError
	if !errors.As(err, &de) {
		t.Fatalf("want *DefinitionError at registration, got %v", err)
	}
	st.CreateObject("a", map[string]string{"balance": "100"})
	out := ex.Execute(context.Background(), "transfer_contradictory", "call-c",
		map[string]any{"from": "a", "amount": "10"}, []string{"a"})
	if out.Class == OutcomeAccepted {
		t.Fatalf("contradictory action must never execute")
	}
	if _, ver, _, _ := st.ObjectState("a"); ver != 0 {
		t.Fatalf("contradictory action changed state")
	}
	if len(st.AuditTrail()) != 0 {
		t.Fatalf("definition error must be resolved before any validation audit is produced")
	}
}

// 声明矛盾优先于一切：即使目标对象已撤销，注册仍先失败。
func TestDefinitionErrorTakesPrecedence(t *testing.T) {
	st := NewStore()
	ex := NewExecutor(st)
	if err := ex.Register(NewContradictoryTransfer()); err == nil {
		t.Fatal("expected definition error")
	}
}

// 正常接受路径：版本号恰好 +1，历史包含该调用，失败轨迹为空。
func TestAcceptedPath(t *testing.T) {
	st, ex := newBankStore()
	st.CreateObject("a", map[string]string{"balance": "100"})
	st.CreateObject("b", map[string]string{"balance": "20"})
	out := ex.Execute(context.Background(), "transfer", "call-ok",
		map[string]any{"from": "a", "to": "b", "amount": "30", "total": "120"},
		[]string{"a", "b"})
	if !out.Accepted {
		t.Fatalf("transfer should be accepted: %+v", out)
	}
	if bal, ver, _ := mustState(t, st, "a"); bal != "70" || ver != 1 {
		t.Fatalf("a state wrong: bal=%s ver=%d", bal, ver)
	}
	if bal, ver, _ := mustState(t, st, "b"); bal != "50" || ver != 1 {
		t.Fatalf("b state wrong: bal=%s ver=%d", bal, ver)
	}
	if h := st.ObjectHistory("a"); len(h) != 1 || h[0].CallID != "call-ok" {
		t.Fatalf("object history wrong: %+v", h)
	}
	if len(st.FailureTrail()) != 0 {
		t.Fatalf("accepted call must leave no failure trail")
	}
}

// 目标对象执行期被并发撤销：返回独立类别，状态不变。
func TestObjectRevoked(t *testing.T) {
	st, ex := newBankStore()
	st.CreateObject("a", map[string]string{"balance": "100"})
	st.CreateObject("b", map[string]string{"balance": "20"})
	st.Revoke("b")
	out := ex.Execute(context.Background(), "transfer", "call-r",
		map[string]any{"from": "a", "to": "b", "amount": "10", "total": "120"},
		[]string{"a", "b"})
	if out.Class != OutcomeObjectRevoked {
		t.Fatalf("want object_revoked, got %+v", out)
	}
	if _, ver, _, _ := st.ObjectState("a"); ver != 0 {
		t.Fatalf("revocation path changed unrelated object")
	}
	if len(st.AcceptedCalls()) != 0 {
		t.Fatalf("revocation path must not be accepted")
	}
}

// 同一调用内计划对同一对象多次写入时，后置校验看到的是最终计划而非中间值。
func TestPostSeesFinalPlanNotIntermediate(t *testing.T) {
	st := NewStore()
	ex := NewExecutor(st)
	// 自定义动作：planner 先把余额写成 -5（中间值），再覆盖回 50（最终值）；
	// 后置要求最终余额 == 50。若后置看到中间值则会误拒。
	act := &Action{
		Type: "doublewrite",
		Pre: func(map[string]any) ([]ConditionClause, error) {
			return []ConditionClause{{Name: "exists", Lits: []Literal{
				{AtomSpec{"obj_exists", []Arg{VarArg("target")}}, true},
			}}}, nil
		},
		Post: func(map[string]any) ([]ConditionClause, error) {
			return []ConditionClause{{Name: "final_balance_50", Lits: []Literal{
				{AtomSpec{"attr_eq", []Arg{VarArg("target"), ConstArg("balance"), ConstArg("50")}}, true},
			}}}, nil
		},
		Plan: func(map[string]any, Snapshot) (*Plan, error) {
			return NewPlanBuilder().
				SetAttr("x", "balance", "-5").
				SetAttr("x", "balance", "50").
				Build(), nil
		},
	}
	ex.MustRegister(act)
	st.CreateObject("x", map[string]string{"balance": "100"})
	out := ex.Execute(context.Background(), "doublewrite", "c1",
		map[string]any{}, []string{"x"})
	if !out.Accepted {
		t.Fatalf("postcondition must observe final plan, got %+v", out)
	}
	if bal, _, _ := mustState(t, st, "x"); bal != "50" {
		t.Fatalf("committed state must be final value, got %s", bal)
	}
}
