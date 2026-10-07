package ontology

import (
	"errors"
	"testing"
)

// 互斥对：A 通过隐含 B 不通过，但二者都被要求通过 → 定义期拒绝。
func TestContradictionMutexPairRejectedAtDefinition(t *testing.T) {
	reg := NewRegistry()
	at := ActionType{
		ID: "bad-mutex",
		Preconditions: []PreCondition{
			{ID: "a", Excludes: []string{"b"}, Eval: func(PreInput) bool { return true }},
			{ID: "b", Eval: func(PreInput) bool { return true }},
		},
	}
	err := reg.Register(at)
	var contra *Contradiction
	if !errors.As(err, &contra) {
		t.Fatalf("expected *Contradiction at registration, got %v", err)
	}
	if contra.Phase != PhasePre {
		t.Fatalf("contradiction phase = %v, want pre", contra.Phase)
	}
	if _, ok := reg.Get("bad-mutex"); ok {
		t.Fatalf("contradictory action must not be registered")
	}
}

// 自斥：条件排除自身 → 定义期拒绝。
func TestContradictionSelfExclusion(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register(ActionType{
		ID: "self-excl",
		Postconditions: []PostCondition{
			{ID: "p", Excludes: []string{"p"}, Eval: func(PostInput) bool { return true }},
		},
	})
	var contra *Contradiction
	if !errors.As(err, &contra) || contra.Phase != PhasePost {
		t.Fatalf("expected post-phase contradiction, got %v", err)
	}
}

// 描述子联合不可满足：x>5 与 x<3 不能同时成立 → 定义期拒绝。
func TestContradictionUnsatisfiableDescriptors(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register(ActionType{
		ID: "unsat",
		Preconditions: []PreCondition{
			{
				ID:         "hi",
				Descriptor: AtomExpr("x", CmpGt, 5),
				Eval:       func(in PreInput) bool { return in.Params["x"].(int64) > 5 },
			},
			{
				ID:         "lo",
				Descriptor: AtomExpr("x", CmpLt, 3),
				Eval:       func(in PreInput) bool { return in.Params["x"].(int64) < 3 },
			},
		},
	})
	var contra *Contradiction
	if !errors.As(err, &contra) {
		t.Fatalf("expected descriptor-unsat contradiction, got %v", err)
	}
}

// 描述子可满足但需跨字段组合：x>5 ∧ y==2 ∧ (x<3 ∨ y==2) 可满足
// （第二个析取支成立）→ 注册成功。
func TestSatisfiableDescriptorCombinationAccepted(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register(ActionType{
		ID: "sat",
		Preconditions: []PreCondition{
			{
				ID:         "c1",
				Descriptor: And(AtomExpr("x", CmpGt, 5), AtomExpr("y", CmpEq, 2)),
				Eval:       func(PreInput) bool { return true },
			},
			{
				ID:         "c2",
				Descriptor: Or(AtomExpr("x", CmpLt, 3), AtomExpr("y", CmpEq, 2)),
				Eval:       func(PreInput) bool { return true },
			},
		},
	})
	if err != nil {
		t.Fatalf("satisfiable declaration must register, got %v", err)
	}
	at, _ := reg.Get("sat")
	an := at.Analysis()
	if an.Contradiction != nil {
		t.Fatalf("unexpected contradiction: %v", an.Contradiction)
	}
	if an.PreAtoms == 0 || an.PreAssigns == 0 {
		t.Fatalf("analysis stats not recorded: %+v", an)
	}
}

// 结构错误：互斥引用未知条件 → 普通错误而非矛盾。
func TestUnknownExclusionReferenceIsStructuralError(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register(ActionType{
		ID: "bad-ref",
		Preconditions: []PreCondition{
			{ID: "a", Excludes: []string{"ghost"}, Eval: func(PreInput) bool { return true }},
		},
	})
	if err == nil {
		t.Fatalf("expected structural error")
	}
	var contra *Contradiction
	if errors.As(err, &contra) {
		t.Fatalf("unknown reference must be a structural error, not contradiction: %v", err)
	}
}

// 运行期矛盾类别：强制注册的矛盾动作在执行时最早被拒绝，
// 优先于目标缺失与前置失败。
func TestContradictionCategoryHasHighestPriorityAtRuntime(t *testing.T) {
	store := NewStore()
	reg := NewRegistry()
	at := ActionType{
		ID: "forced-bad",
		Preconditions: []PreCondition{
			{ID: "a", Excludes: []string{"b"}, Eval: func(PreInput) bool { return false }},
			{ID: "b", Eval: func(PreInput) bool { return false }},
		},
	}
	mustRegister(t, reg, at, PermitContradictory())
	exec := NewExecutor(store, reg)

	// 目标缺失 + 前置必然失败 + 声明矛盾：必须报告声明矛盾。
	res := exec.Execute(Call{
		ID: "c1", ActionType: "forced-bad",
		Params:  map[string]any{"target": "ghost"},
		Targets: []ObjectID{"ghost"},
	})
	if res.Status != StatusRejected || res.Reject.Category != RejectDeclarationContradiction {
		t.Fatalf("declaration contradiction must win, got %+v", res.Reject)
	}
	// 矛盾判定不得产生任何校验记录或状态变化。
	if len(store.ValidationLog()) != 0 {
		t.Fatalf("contradiction must be rejected before any validation")
	}
	if store.CommitSeq() != 0 {
		t.Fatalf("contradiction must not touch committed state")
	}
}

// 矛盾判定开销与历史调用次数无关：分析只在注册时执行一次，
// 任意多次调用后分析计数器不变。
func TestContradictionAnalysisCostIndependentOfInvocations(t *testing.T) {
	store := NewStore()
	reg := NewRegistry()
	mustRegister(t, reg, transferAction())
	mustRegister(t, reg, spendAction(false))
	seedAccounts(store, 2, 1000)
	exec := NewExecutor(store, reg)

	runsBefore := reg.AnalysisRuns()
	if runsBefore != 2 {
		t.Fatalf("analysis runs = %d, want 2 (one per registration)", runsBefore)
	}
	for i := 0; i < 500; i++ {
		res := exec.Execute(transferCall("", accountID(0), accountID(1), 1))
		if !res.Accepted() {
			t.Fatalf("call %d rejected: %+v", i, res.Reject)
		}
		res = exec.Execute(spendCall("", accountID(1), 1))
		if !res.Accepted() {
			t.Fatalf("call %d rejected: %+v", i, res.Reject)
		}
	}
	if got := reg.AnalysisRuns(); got != runsBefore {
		t.Fatalf("analysis ran again after 1000 calls: runs=%d", got)
	}
	// 分析统计只与声明相关：转账动作无描述子，原子数为 0。
	at, _ := reg.Get("transfer")
	if an := at.Analysis(); an.PreAtoms != 0 || an.PostAtoms != 0 {
		t.Fatalf("transfer analysis atoms = (%d,%d), want (0,0)", an.PreAtoms, an.PostAtoms)
	}
}

// 描述子 SAT 求解器的单元测试。
func TestConjunctionSAT(t *testing.T) {
	cases := []struct {
		name  string
		exprs []*Expr
		want  bool
	}{
		{"empty", nil, true},
		{"single", []*Expr{AtomExpr("x", CmpGt, 5)}, true},
		{"contradict-bounds", []*Expr{And(AtomExpr("x", CmpGt, 5), AtomExpr("x", CmpLt, 3))}, false},
		{"boundary-eq", []*Expr{And(AtomExpr("x", CmpGe, 5), AtomExpr("x", CmpLe, 5))}, true},
		{"eq-vs-ne", []*Expr{And(AtomExpr("x", CmpEq, 7), AtomExpr("x", CmpNe, 7))}, false},
		{"ne-excludes-only-point", []*Expr{And(AtomExpr("x", CmpGe, 3), AtomExpr("x", CmpLe, 3), AtomExpr("x", CmpNe, 3))}, false},
		{"not-wrapper", []*Expr{Not(AtomExpr("x", CmpLe, 0)), AtomExpr("x", CmpGt, 0)}, true},
		{"cross-field", []*Expr{And(AtomExpr("x", CmpGt, 5), AtomExpr("y", CmpLt, 3))}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := conjunctionSAT(tc.exprs).Satisfiable; got != tc.want {
				t.Fatalf("satisfiable = %v, want %v", got, tc.want)
			}
		})
	}
}
