package compensate

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func newHarness() (*Graph, *Engine, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	g := NewGraph()
	e := NewEngine(g, NewTextLogger(buf))
	return g, e, buf
}

func mustDeclare(t *testing.T, e *Engine, spec ActionSpec) *Action {
	t.Helper()
	a, err := e.Declare(spec)
	if err != nil {
		t.Fatalf("Declare(%s) unexpected error: %v", spec.ID, err)
	}
	return a
}

func graphGet(g *Graph, obj, prop string) (string, bool) {
	if o, ok := g.Snapshot()[obj]; ok {
		v, exists := o[prop]
		return v, exists
	}
	return "", false
}

// 场景1：无依赖多分支并发补偿，最终图等价于朴素串行参考模型，且回到初始状态。
func TestIndependentBranchesConcurrentCompensationEquivalentToSerial(t *testing.T) {
	g, e, _ := newHarness()
	spec := ActionSpec{ID: "A1", Branches: []BranchSpec{
		{Name: "x", Ops: []WriteOp{{ID: "x0", ObjectID: "ox", Property: "p", Value: "X"}}},
		{Name: "y", Ops: []WriteOp{
			{ID: "y0", ObjectID: "oy", Property: "p", Value: "Y"},
			{ID: "y1", ObjectID: "oy", Property: "q", Value: "Y2"},
		}},
		{Name: "z", Ops: []WriteOp{{ID: "z0", ObjectID: "oz", Property: "p", Value: "Z", FailApply: true}}},
	}}
	a := mustDeclare(t, e, spec)
	rep := a.Execute(context.Background())

	if got := rep.Primary(); got == nil || got.Kind != KindSubOperationFailed {
		t.Fatalf("want primary SUB_OP_FAILED, got %+v", got)
	}
	if len(g.Snapshot()) != 0 {
		t.Fatalf("graph not rolled back: %v", g.Snapshot())
	}
	if len(rep.Compensated["x"]) != 1 || len(rep.Compensated["y"]) != 2 {
		t.Fatalf("compensated map wrong: %+v", rep.Compensated)
	}
	if len(rep.Blocked) != 0 {
		t.Fatalf("no blocked branches expected, got %v", rep.Blocked)
	}

	g2 := NewGraph()
	nr, err := RunNaive(g2, spec, nil)
	if err != nil {
		t.Fatalf("naive: %v", err)
	}
	if len(nr.Final) != 0 {
		t.Fatalf("naive graph not rolled back: %v", nr.Final)
	}
	if fmt.Sprint(nr.Applied) != fmt.Sprint(rep.Applied) {
		t.Fatalf("applied mismatch: naive=%v engine=%v", nr.Applied, rep.Applied)
	}
}

// 场景2：依赖链 b->a, c->b；c 失败后补偿必须严格逆序 c -> b -> a，分支内逆序。
func TestDependencyChainCompensationOrder(t *testing.T) {
	g, e, buf := newHarness()
	spec := ActionSpec{ID: "CHAIN", Branches: []BranchSpec{
		{Name: "a", Ops: []WriteOp{
			{ID: "a0", ObjectID: "oa", Property: "p", Value: "A0"},
			{ID: "a1", ObjectID: "oa", Property: "q", Value: "A1"},
		}},
		{Name: "b", Deps: []string{"a"}, Ops: []WriteOp{
			{ID: "b0", ObjectID: "ob", Property: "p", Value: "B0"},
		}},
		{Name: "c", Deps: []string{"b"}, Ops: []WriteOp{
			{ID: "c0", ObjectID: "oc", Property: "p", Value: "C0"},
			{ID: "c1", ObjectID: "oc", Property: "q", Value: "C1", FailApply: true},
		}},
	}}
	a := mustDeclare(t, e, spec)
	rep := a.Execute(context.Background())

	if len(g.Snapshot()) != 0 {
		t.Fatalf("upstream chain not fully compensated: %v", g.Snapshot())
	}
	var order []string
	seen := map[string]bool{}
	for _, line := range strings.Split(buf.String(), "\n") {
		if !strings.Contains(line, "phase=compensate") {
			continue
		}
		for _, cand := range []string{"c#0", "b#0", "a#1", "a#0"} {
			if strings.Contains(line, "/"+cand+" ") && !seen[cand] {
				seen[cand] = true
				order = append(order, cand)
			}
		}
	}
	want := []string{"c#0", "b#0", "a#1", "a#0"}
	if fmt.Sprint(order) != fmt.Sprint(want) {
		t.Fatalf("compensation order = %v, want %v\nlog:\n%s", order, want, buf.String())
	}
	_ = rep
}

// 场景3：依赖环必须在声明阶段被检测并拒绝，且不产生任何对象图改动。
func TestDependencyCycleRejectedBeforeApply(t *testing.T) {
	g, e, _ := newHarness()
	spec := ActionSpec{ID: "CYC", Branches: []BranchSpec{
		{Name: "a", Deps: []string{"c"}, Ops: []WriteOp{{ObjectID: "oa", Property: "p", Value: "1"}}},
		{Name: "b", Deps: []string{"a"}, Ops: []WriteOp{{ObjectID: "ob", Property: "p", Value: "1"}}},
		{Name: "c", Deps: []string{"b"}, Ops: []WriteOp{{ObjectID: "oc", Property: "p", Value: "1"}}},
	}}
	act, err := e.Declare(spec)
	if act != nil || err == nil {
		t.Fatalf("expected rejection, got action=%v err=%v", act, err)
	}
	rec, ok := err.(ErrorRecord)
	if !ok || rec.Kind != KindDependencyCycle {
		t.Fatalf("want DEPENDENCY_CYCLE, got %#v", err)
	}
	if !strings.Contains(rec.Branch, "a") || !strings.Contains(rec.Branch, "b") || !strings.Contains(rec.Branch, "c") {
		t.Fatalf("cycle members not reported: %+v", rec)
	}
	if len(g.Snapshot()) != 0 {
		t.Fatalf("rejected declaration changed graph: %v", g.Snapshot())
	}
}

// 场景4：下游尚未补偿前，对上游的直接补偿请求必须被拒绝且不改变状态。
func TestDirectCompensationUpstreamDeniedBeforeDownstream(t *testing.T) {
	g, e, _ := newHarness()
	// 构造现场：up、down 已生效（down 依赖 up），另有独立分支触发失败，补偿尚未启动。
	spec := ActionSpec{ID: "DIR", Branches: []BranchSpec{
		{Name: "up", Ops: []WriteOp{{ID: "u0", ObjectID: "ou", Property: "p", Value: "U"}}},
		{Name: "down", Deps: []string{"up"}, Ops: []WriteOp{{ID: "d0", ObjectID: "od", Property: "p", Value: "D"}}},
		{Name: "trigger", Ops: []WriteOp{{ID: "t0", ObjectID: "ot", Property: "p", Value: "T", FailApply: true}}},
	}}
	a := mustDeclare(t, e, spec)
	rt := a.newRuntime()
	rt.runApply(context.Background())

	if err := a.RequestDirectCompensation("up"); err == nil {
		t.Fatalf("expected denial before downstream compensated")
	} else if rec, ok := err.(ErrorRecord); !ok || rec.Kind != KindCompensationDenied {
		t.Fatalf("want COMPENSATION_ORDER_DENIED, got %#v", err)
	}
	if v, ok := graphGet(g, "ou", "p"); !ok || v != "U" {
		t.Fatalf("denial must not change upstream state: %v", g.Snapshot())
	}
	if v, ok := graphGet(g, "od", "p"); !ok || v != "D" {
		t.Fatalf("denial must not change downstream state: %v", g.Snapshot())
	}

	// 直接补偿叶子 down 是允许的；完成后 up 的计数归零，此时再请求 up 应被允许。
	if err := a.RequestDirectCompensation("down"); err != nil {
		t.Fatalf("leaf direct compensation should be allowed: %v", err)
	}
	if err := a.RequestDirectCompensation("up"); err != nil {
		t.Fatalf("upstream should be allowed after downstream done: %v", err)
	}
	rt.startCompensation() // trigger 已失败，无副作用；幂等收口
	if len(g.Snapshot()) != 0 {
		t.Fatalf("expected full rollback, got %v", g.Snapshot())
	}
}

// 场景4b：下游逆操作失败时，对上游的直接补偿仍必须被拒绝（逆序约束不可违反）。
func TestDirectCompensationUpstreamDeniedWhenDownstreamInverseFails(t *testing.T) {
	g, e, _ := newHarness()
	spec := ActionSpec{ID: "DIRF", Branches: []BranchSpec{
		{Name: "up", Ops: []WriteOp{{ID: "u0", ObjectID: "ou", Property: "p", Value: "U"}}},
		{Name: "down", Deps: []string{"up"}, Ops: []WriteOp{
			{ID: "d0", ObjectID: "od", Property: "p", Value: "D", FailCompensate: true},
		}},
		{Name: "trigger", Ops: []WriteOp{{ID: "t0", ObjectID: "ot", Property: "p", Value: "T", FailApply: true}}},
	}}
	a := mustDeclare(t, e, spec)
	rep := a.Execute(context.Background())

	err := a.RequestDirectCompensation("up")
	if err == nil {
		t.Fatalf("upstream must remain blocked when downstream inverse failed; remaining=%v blocked=%v rep=%+v",
			a.rt.branches["up"].remainingDownstream, rep.Blocked, rep.Records)
	}
	rec, ok := err.(ErrorRecord)
	if !ok {
		t.Fatalf("want ErrorRecord, got %#v", err)
	}
	if rec.Kind != KindCompensationDenied {
		t.Fatalf("want COMPENSATION_ORDER_DENIED for blocked upstream, got %v", rec)
	}
	foundBlocked := false
	for _, b := range rep.Blocked {
		if b == "up" {
			foundBlocked = true
		}
	}
	if !foundBlocked {
		t.Fatalf("up should be listed as blocked: %v", rep.Blocked)
	}
	if v, _ := graphGet(g, "ou", "p"); v != "U" {
		t.Fatalf("upstream effect must remain in place: %v", g.Snapshot())
	}
}

// 场景5：两条无依赖分支各自补偿失败，失败信息必须独立、互不遮蔽。
func TestMultipleBranchCompensationFailuresNotMasked(t *testing.T) {
	g, e, _ := newHarness()
	spec := ActionSpec{ID: "MASK", Branches: []BranchSpec{
		{Name: "a", Ops: []WriteOp{
			{ID: "a0", ObjectID: "oa", Property: "p", Value: "A0", FailCompensate: true},
			{ID: "a1", ObjectID: "oa", Property: "q", Value: "A1"},
		}},
		{Name: "b", Ops: []WriteOp{{ID: "b0", ObjectID: "ob", Property: "p", Value: "B0", FailCompensate: true}}},
		{Name: "c", Ops: []WriteOp{{ID: "c0", ObjectID: "oc", Property: "p", Value: "C", FailApply: true}}},
	}}
	a := mustDeclare(t, e, spec)
	rep := a.Execute(context.Background())

	inv := rep.RecordsByKind(KindInverseFailed)
	keys := map[string]bool{}
	for _, r := range inv {
		keys[fmt.Sprintf("%s#%d", r.Branch, r.OpIndex)] = true
	}
	for _, want := range []string{"a#0", "b#0"} {
		if !keys[want] {
			t.Fatalf("missing independent inverse failure %s in %v", want, inv)
		}
	}
	if v, _ := graphGet(g, "oa", "p"); v != "A0" {
		t.Fatalf("failed inverse must leave effect in place: %v", g.Snapshot())
	}
	if v, _ := graphGet(g, "ob", "p"); v != "B0" {
		t.Fatalf("failed inverse must leave effect in place: %v", g.Snapshot())
	}
	if _, exists := graphGet(g, "oa", "q"); exists {
		t.Fatalf("a1 should restore despite a0 failure: %v", g.Snapshot())
	}
}

// 错误优先级：同一报告同时含多类错误时，Primary 必须按固定优先级报告。
func TestErrorPriorityOrdering(t *testing.T) {
	_, e, _ := newHarness()
	spec := ActionSpec{ID: "PRIO", Branches: []BranchSpec{
		{Name: "up", Ops: []WriteOp{{ObjectID: "ou", Property: "p", Value: "U", FailCompensate: true}}},
		{Name: "down", Deps: []string{"up"}, Ops: []WriteOp{{ObjectID: "od", Property: "p", Value: "D", FailApply: true}}},
	}}
	a := mustDeclare(t, e, spec)
	rep := a.Execute(context.Background())
	// down 自身失败 (SUB_OP_FAILED) 使 up/down 都无需或部分补偿；up 已生效但被 down 的关系阻塞，
	// 且 up 的逆操作失败。报告必须同时含 SUB_OP_FAILED 与 INVERSE_FAILED，前者优先。
	kinds := map[ErrKind]bool{}
	for _, r := range rep.Records {
		kinds[r.Kind] = true
	}
	if !kinds[KindSubOperationFailed] || !kinds[KindInverseFailed] {
		t.Fatalf("expected both failure classes, got %v", rep.Records)
	}
	if rep.Primary().Kind != KindSubOperationFailed {
		t.Fatalf("priority wrong: %v", rep.Primary())
	}
}

// 跨动作可串行化：两个动作写同一槽位并各自失败回滚，并发执行结果必须等价于某种串行顺序（最终为空）。
func TestCrossActionSerializability(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		g := NewGraph()
		mk := func(id string, triggerObj string) *Action {
			e := NewEngine(g, nil)
			spec := ActionSpec{ID: id, Branches: []BranchSpec{
				{Name: "shared", Ops: []WriteOp{{ID: "s", ObjectID: "k", Property: "p", Value: id}}},
				{Name: "trigger", Ops: []WriteOp{{ObjectID: triggerObj, Property: "p", Value: "x", FailApply: true}}},
			}}
			a, err := e.Declare(spec)
			if err != nil {
				t.Fatalf("declare: %v", err)
			}
			return a
		}
		a1 := mk("ACT1", "t1")
		a2 := mk("ACT2", "t2")
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); a1.Execute(context.Background()) }()
		go func() { defer wg.Done(); a2.Execute(context.Background()) }()
		wg.Wait()
		if len(g.Snapshot()) != 0 {
			t.Fatalf("iter %d: non-serializable leftover %v", iter, g.Snapshot())
		}
	}
}
