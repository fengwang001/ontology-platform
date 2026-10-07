package lifecycle_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"ontology/hooks"
	"ontology/lcerr"
	"ontology/lifecycle"
)

// testHook 是可编程的测试钩子：记录触发顺序、写副作用、按配置失败。
type testHook struct {
	name     string
	commit   hooks.CommitSemantics
	fired    *[]string
	note     string
	failWith error
}

func (h testHook) Name() string { return h.name }

func (h testHook) Semantics() hooks.CommitSemantics { return h.commit }

func (h testHook) Validate(_ context.Context, tc *hooks.Context) error {
	*h.fired = append(*h.fired, h.name)
	if h.note != "" {
		tc.Record(h.note)
	}
	return h.failWith
}

func mustSchema(t *testing.T, cfg lifecycle.SchemaConfig) *lifecycle.Schema {
	t.Helper()
	s, err := lifecycle.NewSchema(cfg)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	return s
}

func mustInstantiate(t *testing.T, m *lifecycle.Machine, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := m.Instantiate(id); err != nil {
			t.Fatalf("Instantiate(%q): %v", id, err)
		}
	}
}

func phaseOf(t *testing.T, m *lifecycle.Machine, id string) lifecycle.Phase {
	t.Helper()
	p, err := m.PhaseOf(id)
	if err != nil {
		t.Fatalf("PhaseOf(%q): %v", id, err)
	}
	return p
}

func journalOf(t *testing.T, m *lifecycle.Machine, id string) []hooks.SideEffect {
	t.Helper()
	j, err := m.Journal(id)
	if err != nil {
		t.Fatalf("Journal(%q): %v", id, err)
	}
	return j
}

// ringSchema 是标准测试图：A -> B -> C -> A 构成环，B -> D 进入终态 D。
func ringSchema(t *testing.T) *lifecycle.Schema {
	t.Helper()
	return mustSchema(t, lifecycle.SchemaConfig{
		Initial: "A",
		Phases:  []lifecycle.Phase{"A", "B", "C", "D"},
		Edges: []lifecycle.Edge{
			{From: "A", To: "B"},
			{From: "B", To: "C"},
			{From: "C", To: "A"},
			{From: "B", To: "D"},
		},
		Terminals: []lifecycle.Phase{"D"},
	})
}

// TestValidationOrder 验证四类拒绝原因按固定优先级只报第一个：
// 参数非法 > 起始阶段为终态 > 转移关系未允许 > 钩子校验失败。
func TestValidationOrder(t *testing.T) {
	ctx := context.Background()
	var fired []string
	boom := errors.New("boom")
	reg := hooks.NewRegistry()
	reg.OnEnter("B", testHook{name: "enter-B", commit: hooks.RollbackOnFailure, fired: &fired})
	reg.OnTransition("A", "B", testHook{
		name: "t-A-B", commit: hooks.RollbackOnFailure, fired: &fired,
		failWith: boom,
	})

	m := lifecycle.NewMachine(ringSchema(t), reg)
	mustInstantiate(t, m, "obj-1")

	// 1. 参数非法：实例不存在 / 目标阶段未声明。
	err := m.Transition(ctx, "ghost", "B")
	if !lcerr.IsKind(err, lcerr.KindInvalidArgument) {
		t.Fatalf("missing instance: want invalid_argument, got %v", err)
	}
	err = m.Transition(ctx, "obj-1", "X")
	if !lcerr.IsKind(err, lcerr.KindInvalidArgument) {
		t.Fatalf("undeclared target: want invalid_argument, got %v", err)
	}

	// 2. 转移关系未允许（A -> C 未声明），且先于钩子校验。
	err = m.Transition(ctx, "obj-1", "C")
	if !lcerr.IsKind(err, lcerr.KindTransitionNotAllowed) {
		t.Fatalf("undeclared edge: want transition_not_allowed, got %v", err)
	}

	// 3. 钩子校验失败（A -> B 允许，但转移钩子失败）。
	err = m.Transition(ctx, "obj-1", "B")
	if !lcerr.IsKind(err, lcerr.KindHookFailed) {
		t.Fatalf("hook failure: want hook_failed, got %v", err)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("hook failure should wrap cause, got %v", err)
	}
	if got := phaseOf(t, m, "obj-1"); got != "A" {
		t.Fatalf("rejected transition changed phase: got %q want A", got)
	}

	// 4. 起始阶段为终态：优先于钩子校验，钩子不得触发。
	// 用无失败钩子的新状态机把实例推进到终态 D。
	reg2 := hooks.NewRegistry()
	reg2.OnEnter("D", testHook{name: "enter-D", commit: hooks.RollbackOnFailure, fired: &fired})
	reg2.OnTransition("D", "D", testHook{name: "trans-D-D", commit: hooks.RollbackOnFailure, fired: &fired})
	m = lifecycle.NewMachine(ringSchema(t), reg2)
	mustInstantiate(t, m, "obj-2")
	fired = nil
	if err := m.Transition(ctx, "obj-2", "B"); err != nil {
		t.Fatalf("obj-2 A->B: %v", err)
	}
	if err := m.Transition(ctx, "obj-2", "D"); err != nil {
		t.Fatalf("obj-2 B->D: %v", err)
	}
	before := len(fired)
	err = m.Transition(ctx, "obj-2", "D")
	if !lcerr.IsKind(err, lcerr.KindTerminalSource) {
		t.Fatalf("terminal source: want terminal_source, got %v", err)
	}
	if len(fired) != before {
		t.Fatalf("terminal rejection must not fire hooks, fired=%v", fired)
	}
	if got := phaseOf(t, m, "obj-2"); got != "D" {
		t.Fatalf("terminal rejection changed phase: got %q want D", got)
	}
}

// TestHookFiringOrder 验证同一次转移命中两类钩子时，
// 具体转移钩子先于目标阶段进入钩子触发，同类内按注册顺序。
func TestHookFiringOrder(t *testing.T) {
	ctx := context.Background()
	var fired []string
	reg := hooks.NewRegistry()
	reg.OnEnter("B", testHook{name: "enter-B-1", commit: hooks.RollbackOnFailure, fired: &fired})
	reg.OnTransition("A", "B", testHook{name: "trans-A-B-1", commit: hooks.RollbackOnFailure, fired: &fired})
	reg.OnEnter("B", testHook{name: "enter-B-2", commit: hooks.RollbackOnFailure, fired: &fired})
	reg.OnTransition("A", "B", testHook{name: "trans-A-B-2", commit: hooks.RollbackOnFailure, fired: &fired})

	m := lifecycle.NewMachine(ringSchema(t), reg)
	mustInstantiate(t, m, "obj")
	if err := m.Transition(ctx, "obj", "B"); err != nil {
		t.Fatalf("A->B: %v", err)
	}
	want := []string{"trans-A-B-1", "trans-A-B-2", "enter-B-1", "enter-B-2"}
	if !reflect.DeepEqual(fired, want) {
		t.Fatalf("firing order: got %v want %v", fired, want)
	}

	hist, err := m.History("obj")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 1 || !reflect.DeepEqual(hist[0].Hooks, want) {
		t.Fatalf("history hooks: got %+v want hooks %v", hist, want)
	}
	if hist[0].From != "A" || hist[0].To != "B" {
		t.Fatalf("history entry: got %+v", hist[0])
	}
}

// TestSelfTransition 验证自转移语义：
//   - 自转移必须显式声明，否则按「转移关系未允许」拒绝；
//   - 进入钩子在自转移时仍触发；
//   - 具体转移钩子只在注册了「阶段到自身」的钩子时触发，
//     不因阶段相同而默认跳过或默认触发。
func TestSelfTransition(t *testing.T) {
	ctx := context.Background()
	schema := mustSchema(t, lifecycle.SchemaConfig{
		Initial: "P",
		Phases:  []lifecycle.Phase{"P", "Q"},
		Edges: []lifecycle.Edge{
			{From: "P", To: "P"}, // 显式声明的自转移
			{From: "P", To: "Q"},
			// 注意：Q -> Q 未声明
		},
	})

	t.Run("enter hook fires on self transition", func(t *testing.T) {
		var fired []string
		reg := hooks.NewRegistry()
		reg.OnEnter("P", testHook{name: "enter-P", commit: hooks.RollbackOnFailure, fired: &fired})
		m := lifecycle.NewMachine(schema, reg)
		mustInstantiate(t, m, "obj")
		if err := m.Transition(ctx, "obj", "P"); err != nil {
			t.Fatalf("P->P: %v", err)
		}
		if want := []string{"enter-P"}; !reflect.DeepEqual(fired, want) {
			t.Fatalf("got %v want %v", fired, want)
		}
		if got := phaseOf(t, m, "obj"); got != "P" {
			t.Fatalf("phase: got %q want P", got)
		}
	})

	t.Run("transition hook fires only when declared on self edge", func(t *testing.T) {
		var fired []string
		reg := hooks.NewRegistry()
		reg.OnEnter("P", testHook{name: "enter-P", commit: hooks.RollbackOnFailure, fired: &fired})
		reg.OnTransition("P", "P", testHook{name: "trans-P-P", commit: hooks.RollbackOnFailure, fired: &fired})
		m := lifecycle.NewMachine(schema, reg)
		mustInstantiate(t, m, "obj")
		if err := m.Transition(ctx, "obj", "P"); err != nil {
			t.Fatalf("P->P: %v", err)
		}
		// 具体转移钩子在先，进入钩子在后。
		if want := []string{"trans-P-P", "enter-P"}; !reflect.DeepEqual(fired, want) {
			t.Fatalf("got %v want %v", fired, want)
		}
	})

	t.Run("enter hook is not mistaken for transition hook", func(t *testing.T) {
		// 只注册进入钩子时，不得默认合成一个具体转移钩子。
		var fired []string
		reg := hooks.NewRegistry()
		reg.OnEnter("P", testHook{name: "enter-P", commit: hooks.RollbackOnFailure, fired: &fired})
		m := lifecycle.NewMachine(schema, reg)
		mustInstantiate(t, m, "obj")
		if err := m.Transition(ctx, "obj", "P"); err != nil {
			t.Fatalf("P->P: %v", err)
		}
		hist, err := m.History("obj")
		if err != nil {
			t.Fatalf("History: %v", err)
		}
		if want := []string{"enter-P"}; !reflect.DeepEqual(hist[0].Hooks, want) {
			t.Fatalf("got %v want %v", hist[0].Hooks, want)
		}
	})

	t.Run("undeclared self transition rejected", func(t *testing.T) {
		var fired []string
		reg := hooks.NewRegistry()
		reg.OnEnter("Q", testHook{name: "enter-Q", commit: hooks.RollbackOnFailure, fired: &fired})
		m := lifecycle.NewMachine(schema, reg)
		mustInstantiate(t, m, "obj")
		if err := m.Transition(ctx, "obj", "Q"); err != nil {
			t.Fatalf("P->Q: %v", err)
		}
		fired = fired[:0]
		err := m.Transition(ctx, "obj", "Q") // Q -> Q 未声明
		if !lcerr.IsKind(err, lcerr.KindTransitionNotAllowed) {
			t.Fatalf("want transition_not_allowed, got %v", err)
		}
		if len(fired) != 0 {
			t.Fatalf("rejected self transition fired hooks: %v", fired)
		}
	})
}

// TestTerminalBlocksAllOutgoing 验证终态拒绝一切转出（含转到自身），
// 且终态限制优先于钩子校验：钩子不得被触发。
func TestTerminalBlocksAllOutgoing(t *testing.T) {
	ctx := context.Background()
	schema := mustSchema(t, lifecycle.SchemaConfig{
		Initial: "A",
		Phases:  []lifecycle.Phase{"A", "T"},
		Edges: []lifecycle.Edge{
			{From: "A", To: "T"},
			{From: "T", To: "T"}, // 即使声明了自转移
			{From: "T", To: "A"}, // 即使声明了回边
		},
		Terminals: []lifecycle.Phase{"T"},
	})
	var fired []string
	reg := hooks.NewRegistry()
	reg.OnTransition("T", "T", testHook{name: "trans-T-T", commit: hooks.RollbackOnFailure, fired: &fired})
	reg.OnTransition("T", "A", testHook{name: "trans-T-A", commit: hooks.RollbackOnFailure, fired: &fired})
	reg.OnEnter("T", testHook{name: "enter-T", commit: hooks.RollbackOnFailure, fired: &fired})
	reg.OnEnter("A", testHook{name: "enter-A", commit: hooks.RollbackOnFailure, fired: &fired})

	m := lifecycle.NewMachine(schema, reg)
	mustInstantiate(t, m, "obj")
	if err := m.Transition(ctx, "obj", "T"); err != nil {
		t.Fatalf("A->T: %v", err)
	}
	// 进入终态时触发过 enter-T，此后任何转出都不得再触发钩子。
	fired = fired[:0]

	for _, target := range []lifecycle.Phase{"T", "A"} {
		err := m.Transition(ctx, "obj", target)
		if !lcerr.IsKind(err, lcerr.KindTerminalSource) {
			t.Fatalf("T->%s: want terminal_source, got %v", target, err)
		}
		if len(fired) != 0 {
			t.Fatalf("T->%s: terminal check must precede hooks, fired=%v", target, fired)
		}
		if got := phaseOf(t, m, "obj"); got != "T" {
			t.Fatalf("T->%s: phase changed to %q", target, got)
		}
	}
}

// TestHookFailureAtomicity 验证钩子失败时转移整体不生效：
// 阶段保持转移前的值；AutoCommit 钩子的副作用保留，
// RollbackOnFailure 钩子的副作用随转移回滚。
func TestHookFailureAtomicity(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("validation failed")
	var fired []string
	reg := hooks.NewRegistry()
	// 顺序：auto-1(记日志) -> rollback-1(记日志) -> auto-2(记日志并失败)
	reg.OnTransition("A", "B", testHook{
		name: "auto-1", commit: hooks.AutoCommit, fired: &fired, note: "log-from-auto-1",
	})
	reg.OnTransition("A", "B", testHook{
		name: "rollback-1", commit: hooks.RollbackOnFailure, fired: &fired, note: "log-from-rollback-1",
	})
	reg.OnEnter("B", testHook{
		name: "auto-2", commit: hooks.AutoCommit, fired: &fired, note: "log-from-auto-2",
		failWith: boom,
	})

	m := lifecycle.NewMachine(ringSchema(t), reg)
	mustInstantiate(t, m, "obj")

	err := m.Transition(ctx, "obj", "B")
	if !lcerr.IsKind(err, lcerr.KindHookFailed) {
		t.Fatalf("want hook_failed, got %v", err)
	}
	if got := phaseOf(t, m, "obj"); got != "A" {
		t.Fatalf("phase must stay at pre-transition value, got %q", got)
	}
	if want := []string{"auto-1", "rollback-1", "auto-2"}; !reflect.DeepEqual(fired, want) {
		t.Fatalf("fired: got %v want %v", fired, want)
	}
	// 只有 AutoCommit 钩子的副作用进入日志；失败钩子自身也是 AutoCommit。
	journal := journalOf(t, m, "obj")
	var notes []string
	for _, e := range journal {
		notes = append(notes, e.Note)
	}
	wantNotes := []string{"log-from-auto-1", "log-from-auto-2"}
	if !reflect.DeepEqual(notes, wantNotes) {
		t.Fatalf("journal: got %v want %v", notes, wantNotes)
	}
	// 失败的转移不进入阶段历史。
	hist, err := m.History("obj")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 0 {
		t.Fatalf("failed transition must not appear in history: %+v", hist)
	}
}

// TestSuccessCommitsAllEffects 验证全部钩子通过时，
// 所有副作用按触发顺序提交，阶段更新为目标阶段。
func TestSuccessCommitsAllEffects(t *testing.T) {
	ctx := context.Background()
	var fired []string
	reg := hooks.NewRegistry()
	reg.OnTransition("A", "B", testHook{
		name: "t1", commit: hooks.RollbackOnFailure, fired: &fired, note: "n1",
	})
	reg.OnEnter("B", testHook{
		name: "e1", commit: hooks.AutoCommit, fired: &fired, note: "n2",
	})
	m := lifecycle.NewMachine(ringSchema(t), reg)
	mustInstantiate(t, m, "obj")
	if err := m.Transition(ctx, "obj", "B"); err != nil {
		t.Fatalf("A->B: %v", err)
	}
	if got := phaseOf(t, m, "obj"); got != "B" {
		t.Fatalf("phase: got %q want B", got)
	}
	journal := journalOf(t, m, "obj")
	var notes []string
	for _, e := range journal {
		notes = append(notes, e.Note)
	}
	if want := []string{"n1", "n2"}; !reflect.DeepEqual(notes, want) {
		t.Fatalf("journal: got %v want %v", notes, want)
	}
}

// TestSchemaValidation 验证生命周期定义自身的参数校验。
func TestSchemaValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  lifecycle.SchemaConfig
	}{
		{"empty phases", lifecycle.SchemaConfig{Initial: "A"}},
		{"initial not declared", lifecycle.SchemaConfig{
			Initial: "Z", Phases: []lifecycle.Phase{"A"},
		}},
		{"duplicate phase", lifecycle.SchemaConfig{
			Initial: "A", Phases: []lifecycle.Phase{"A", "A"},
		}},
		{"edge to undeclared", lifecycle.SchemaConfig{
			Initial: "A", Phases: []lifecycle.Phase{"A"},
			Edges: []lifecycle.Edge{{From: "A", To: "B"}},
		}},
		{"duplicate edge", lifecycle.SchemaConfig{
			Initial: "A", Phases: []lifecycle.Phase{"A"},
			Edges: []lifecycle.Edge{{From: "A", To: "A"}, {From: "A", To: "A"}},
		}},
		{"terminal not declared", lifecycle.SchemaConfig{
			Initial: "A", Phases: []lifecycle.Phase{"A"}, Terminals: []lifecycle.Phase{"T"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := lifecycle.NewSchema(tc.cfg)
			if !lcerr.IsKind(err, lcerr.KindInvalidArgument) {
				t.Fatalf("want invalid_argument, got %v", err)
			}
		})
	}
}

// TestDuplicateInstance 验证重复创建实例属于参数非法。
func TestDuplicateInstance(t *testing.T) {
	m := lifecycle.NewMachine(ringSchema(t), nil)
	mustInstantiate(t, m, "obj")
	err := m.Instantiate("obj")
	if !lcerr.IsKind(err, lcerr.KindInvalidArgument) {
		t.Fatalf("want invalid_argument, got %v", err)
	}
}
