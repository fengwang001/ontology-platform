package compensation

import (
	"bytes"
	"context"
	"fmt"
	"testing"
)

func newHarness(t *testing.T, nObjects int) (*Graph, *Executor, *bytes.Buffer) {
	t.Helper()
	g := NewGraph()
	for i := 0; i < nObjects; i++ {
		id := fmt.Sprintf("o%d", i)
		g.AddObject(id, map[string]any{"a": 0, "b": "x"})
	}
	var buf bytes.Buffer
	tr := &LogTracer{W: &buf}
	exec := New(g, tr)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("\n%s", buf.String())
		}
	})
	return g, exec, &buf
}

func setAction(id string, checks ...bool) *Action {
	ops := make([]SubOp, len(checks))
	for i, ok := range checks {
		ops[i] = SubOp{
			Kind:     OpSetProperties,
			ObjectID: fmt.Sprintf("o%d", i),
			Sets:     map[string]any{"a": i + 1},
			Check: func(v bool) func() bool {
				return func() bool { return v }
			}(ok),
		}
	}
	return &Action{ID: id, Ops: ops}
}

// TestSingleStepFailure 单环节失败：失败步骤不补偿，此前步骤被逆序撤销。
func TestSingleStepFailure(t *testing.T) {
	g, exec, _ := newHarness(t, 4)
	// 0、1 生效，2 业务拒绝。
	act := setAction("A", true, true, false, true)
	out := exec.Execute(context.Background(), act)

	if out.Category != CategoryBusinessRejected || out.FailedStep != 2 {
		t.Fatalf("want BUSINESS_REJECTED at step 2, got %s step %d", out.Category, out.FailedStep)
	}
	if len(out.Applied) != 2 || out.Applied[0].Index != 0 || out.Applied[1].Index != 1 {
		t.Fatalf("want applied [0 1], got %+v", out.Applied)
	}
	if len(out.CompFailures) != 0 {
		t.Fatalf("want clean compensation, got failures %+v", out.CompFailures)
	}
	wantComp := []int{1, 0}
	if fmt.Sprint(out.Compensated) != fmt.Sprint(wantComp) {
		t.Fatalf("want reverse-order compensated %v, got %v", wantComp, out.Compensated)
	}
	for i := 0; i < 4; i++ {
		v, _ := g.Prop(fmt.Sprintf("o%d", i), "a")
		if v != 0 {
			t.Fatalf("o%d.a = %v, want restored 0", i, v)
		}
		ver, _ := g.ObjectVersion(fmt.Sprintf("o%d", i))
		if ver != 0 {
			t.Fatalf("o%d.version = %d, want 0 after clean rollback", i, ver)
		}
	}
}

// TestFailureAtEveryPosition 多环节失败覆盖每个失败位置。
func TestFailureAtEveryPosition(t *testing.T) {
	for failAt := 0; failAt < 5; failAt++ {
		t.Run(fmt.Sprintf("failAt=%d", failAt), func(t *testing.T) {
			g, exec, _ := newHarness(t, 6)
			checks := make([]bool, 5)
			for i := range checks {
				checks[i] = i != failAt
			}
			out := exec.Execute(context.Background(), setAction("A", checks...))
			if out.FailedStep != failAt {
				t.Fatalf("failed step = %d, want %d", out.FailedStep, failAt)
			}
			if len(out.Applied) != failAt {
				t.Fatalf("applied = %d, want %d", len(out.Applied), failAt)
			}
			for i := 0; i < 5; i++ {
				v, _ := g.Prop(fmt.Sprintf("o%d", i), "a")
				if v != 0 {
					t.Fatalf("o%d.a = %v, want 0 (all effects rolled back or never applied)", i, v)
				}
			}
			// 后续步骤 3、4 永不执行：不应出现在任何记录里。
			for _, ap := range out.Applied {
				if ap.Index > failAt {
					t.Fatalf("step %d must never execute", ap.Index)
				}
			}
		})
	}
}

// TestInverseFailureDuringCompensation 逆操作返回失败：补偿不中止，
// 其余逆操作继续执行，失败实例状态冻结并被标记污染。
func TestInverseFailureDuringCompensation(t *testing.T) {
	g, exec, _ := newHarness(t, 5)
	act := &Action{ID: "A", Ops: []SubOp{
		{Kind: OpSetProperties, ObjectID: "o0", Sets: map[string]any{"a": 100}},
		// 步骤 1 的逆操作将失败：其状态应冻结在生效后的值 101。
		{Kind: OpSetProperties, ObjectID: "o1", Sets: map[string]any{"a": 101},
			InjectInverseFailure: true},
		{Kind: OpSetProperties, ObjectID: "o2", Sets: map[string]any{"a": 102}},
		{Check: func() bool { return false }}, // 步骤 3 业务失败触发补偿
	}}
	out := exec.Execute(context.Background(), act)

	if out.Category != CategoryCompensationFailed {
		t.Fatalf("want COMPENSATION_FAILED, got %s", out.Category)
	}
	failIdx := -1
	for _, rec := range out.CompFailures {
		if rec.Index == 1 && !rec.OK && !rec.Skipped {
			failIdx = 1
		}
	}
	if failIdx == -1 {
		t.Fatalf("want a hard failure record for step 1, got %+v", out.CompFailures)
	}
	if v, _ := g.Prop("o0", "a"); v != 0 {
		t.Fatalf("o0.a = %v, want rolled-back 0 (compensation must continue past failures)", v)
	}
	if v, _ := g.Prop("o2", "a"); v != 0 {
		t.Fatalf("o2.a = %v, want rolled-back 0", v)
	}
	if v, _ := g.Prop("o1", "a"); v != 101 {
		t.Fatalf("o1.a = %v, want frozen at 101 (failed inverse must not mutate)", v)
	}
	if info, tainted := g.Tainted("o1"); !tainted || info.EarliestStep != 1 {
		t.Fatalf("o1 must be tainted with earliest step 1, got %+v %v", info, tainted)
	}
	if _, tainted := g.Tainted("o0"); tainted {
		t.Fatal("o0 compensated cleanly and must not be tainted")
	}
}

// TestInversePanicDuringCompensation 逆操作抛异常：异常被转换为失败记录，
// 绝不穿透补偿流程，其余逆操作继续执行。
func TestInversePanicDuringCompensation(t *testing.T) {
	g, exec, _ := newHarness(t, 5)
	act := &Action{ID: "A", Ops: []SubOp{
		{Kind: OpSetProperties, ObjectID: "o0", Sets: map[string]any{"a": 10}},
		{Kind: OpSetProperties, ObjectID: "o1", Sets: map[string]any{"a": 11},
			InjectInversePanic: true},
		{Kind: OpSetProperties, ObjectID: "o2", Sets: map[string]any{"a": 12}},
		{Check: func() bool { return false }},
	}}
	out := exec.Execute(context.Background(), act)

	if out.Category != CategoryCompensationFailed {
		t.Fatalf("want COMPENSATION_FAILED, got %s", out.Category)
	}
	var found bool
	for _, rec := range out.CompFailures {
		if rec.Index == 1 && rec.Panicked {
			found = true
		}
	}
	if !found {
		t.Fatalf("want a panicked failure record for step 1, got %+v", out.CompFailures)
	}
	if v, _ := g.Prop("o0", "a"); v != 0 {
		t.Fatalf("o0.a = %v, want 0", v)
	}
	if v, _ := g.Prop("o2", "a"); v != 0 {
		t.Fatalf("o2.a = %v, want 0", v)
	}
	if v, _ := g.Prop("o1", "a"); v != 11 {
		t.Fatalf("o1.a = %v, want frozen 11", v)
	}
	if _, tainted := g.Tainted("o1"); !tainted {
		t.Fatal("o1 must be tainted after panicked inverse")
	}
}

// TestSkipAfterPoisoned 失败实例之后（更早生效）涉及同一实例的逆操作被跳过，
// 状态保持补偿失败前的样子。
func TestSkipAfterPoisoned(t *testing.T) {
	g, exec, _ := newHarness(t, 4)
	act := &Action{ID: "A", Ops: []SubOp{
		{Kind: OpSetProperties, ObjectID: "o1", Sets: map[string]any{"a": 1}},
		{Kind: OpSetProperties, ObjectID: "o0", Sets: map[string]any{"a": 2}},
		// 步骤 2 逆操作失败污染 o1；补偿继续到步骤 0（同涉 o1）时必须跳过。
		{Kind: OpSetProperties, ObjectID: "o1", Sets: map[string]any{"a": 3},
			InjectInverseFailure: true},
		{Check: func() bool { return false }},
	}}
	out := exec.Execute(context.Background(), act)
	if out.Category != CategoryCompensationFailed {
		t.Fatalf("want COMPENSATION_FAILED, got %s", out.Category)
	}
	var skippedStep0 bool
	for _, rec := range out.CompFailures {
		if rec.Index == 0 && rec.Skipped {
			skippedStep0 = true
		}
	}
	if !skippedStep0 {
		t.Fatalf("step 0 must be skipped as o1 already poisoned, got %+v", out.CompFailures)
	}
	// o1 在步骤 2 生效后值为 3；该步逆操作失败，状态冻结在 3，步骤 0 被跳过。
	if v, _ := g.Prop("o1", "a"); v != 3 {
		t.Fatalf("o1.a = %v, want frozen value 3", v)
	}
	if v, _ := g.Prop("o0", "a"); v != 0 {
		t.Fatalf("o0.a = %v, want 0", v)
	}
}

// TestAllStepsSucceed 全部成功：无补偿、无拒绝。
func TestAllStepsSucceed(t *testing.T) {
	g, exec, _ := newHarness(t, 3)
	out := exec.Execute(context.Background(), setAction("A", true, true, true))
	if out.Category != CategoryOK {
		t.Fatalf("want OK, got %s", out.Category)
	}
	for i := 0; i < 3; i++ {
		if v, _ := g.Prop(fmt.Sprintf("o%d", i), "a"); v != i+1 {
			t.Fatalf("o%d.a = %v, want %d", i, v, i+1)
		}
	}
}

// TestLinkCreateDeleteRollback 链接创建/删除的逆操作恢复链接状态与端点时钟。
func TestLinkCreateDeleteRollback(t *testing.T) {
	g, exec, _ := newHarness(t, 3)
	act := &Action{ID: "A", Ops: []SubOp{
		{Kind: OpCreateLink, LinkID: "L1", From: "o0", To: "o1", LinkType: "rel"},
		{Kind: OpSetProperties, ObjectID: "o2", Sets: map[string]any{"a": 9}},
		{Check: func() bool { return false }},
	}}
	out := exec.Execute(context.Background(), act)
	if out.Category != CategoryBusinessRejected {
		t.Fatalf("want BUSINESS_REJECTED, got %s", out.Category)
	}
	if g.LinkAlive("L1") {
		t.Fatal("L1 must be removed after compensating create")
	}
	for _, id := range []string{"o0", "o1", "o2"} {
		if v, _ := g.ObjectVersion(id); v != 0 {
			t.Fatalf("%s version = %d, want 0", id, v)
		}
		if c, _ := g.ObjectClock(id); c != 0 {
			t.Fatalf("%s clock = %d, want 0", id, c)
		}
	}

	// 先建一条链接，再让删除操作随补偿回滚：链接应恢复存活。
	act2 := &Action{ID: "B", Ops: []SubOp{
		{Kind: OpDeleteLink, LinkID: "L1"},
		{Check: func() bool { return false }},
	}}
	// 重建链接作为前置状态
	setup := &Action{ID: "S", Ops: []SubOp{
		{Kind: OpCreateLink, LinkID: "L1", From: "o0", To: "o1", LinkType: "rel"},
	}}
	if o := exec.Execute(context.Background(), setup); o.Category != CategoryOK {
		t.Fatalf("setup failed: %s", o.Category)
	}
	out2 := exec.Execute(context.Background(), act2)
	if out2.Category != CategoryBusinessRejected {
		t.Fatalf("want BUSINESS_REJECTED, got %s", out2.Category)
	}
	if !g.LinkAlive("L1") {
		t.Fatal("L1 must be restored alive after compensating delete")
	}
}
