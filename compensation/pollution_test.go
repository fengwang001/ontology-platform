package compensation

import (
	"context"
	"fmt"
	"testing"
)

func poisonGraph(t *testing.T) (*Graph, *Executor) {
	t.Helper()
	g, exec, _ := newHarness(t, 6)
	// 动作：0、1 生效，1 的逆操作失败，2 业务失败触发补偿。
	act := &Action{ID: "poison", Ops: []SubOp{
		{Kind: OpSetProperties, ObjectID: "o0", Sets: map[string]any{"a": 1}},
		{Kind: OpSetProperties, ObjectID: "o1", Sets: map[string]any{"a": 2},
			InjectInverseFailure: true},
		{Check: func() bool { return false }},
	}}
	out := exec.Execute(context.Background(), act)
	if out.Category != CategoryCompensationFailed {
		t.Fatalf("setup: want COMPENSATION_FAILED, got %s", out.Category)
	}
	return g, exec
}

// TestContaminatedRejectsNewActionBeforeAnyEffect 污染态实例参与的新动作
// 必须在执行任何子操作之前被拒绝。
func TestContaminatedRejectsNewActionBeforeAnyEffect(t *testing.T) {
	g, exec := poisonGraph(t)

	// 新动作先改干净对象 o3，再触达污染对象 o1：
	// 若拒绝发生在预检，则 o3 必须保持不变。
	act := &Action{ID: "new", Ops: []SubOp{
		{Kind: OpSetProperties, ObjectID: "o3", Sets: map[string]any{"a": 77}},
		{Kind: OpSetProperties, ObjectID: "o1", Sets: map[string]any{"a": 78}},
	}}
	before, _ := g.ObjectVersion("o3")
	out := exec.Execute(context.Background(), act)

	if out.Category != CategoryContaminated {
		t.Fatalf("want CONTAMINATED, got %s", out.Category)
	}
	if !out.RejectedBeforeEffect {
		t.Fatal("rejection must be flagged as before-any-effect")
	}
	if v, _ := g.Prop("o3", "a"); v != 0 {
		t.Fatalf("o3.a = %v, want 0: no effect allowed before contaminated rejection", v)
	}
	after, _ := g.ObjectVersion("o3")
	if after != before {
		t.Fatalf("o3 version changed %d -> %d despite contaminated rejection", before, after)
	}
	if len(out.Applied) != 0 {
		t.Fatalf("no steps may apply, got %+v", out.Applied)
	}
}

// TestEarliestContaminationNumberNeverOverwritten 同一实例多次卷入污染：
// 查询必须永远返回第一次污染补偿里最早失败的子操作编号。
func TestEarliestContaminationNumberNeverOverwritten(t *testing.T) {
	g, exec := poisonGraph(t)
	info, ok := g.Tainted("o1")
	if !ok || info.EarliestStep != 1 {
		t.Fatalf("initial taint want step 1, got %+v ok=%v", info, ok)
	}

	// o1 已污染，无法通过正常动作再次制造它的副作用；改为直接驱动一次补偿，
	// 让 o1 在另一次补偿的更晚/更早编号上再次“补偿失败”：
	// 手工构造栈模拟一次新补偿（编号更新、失败步骤编号不同）。
	otherStack := []*inverse{
		{step: 4, entry: g.nextEntry(), objects: []string{"o1"},
			apply: func() error { return nil }, failNow: true},
	}
	exec.compensate("second", otherStack)
	g.commitTaints([]CompRecord{{Index: 4, Entry: otherStack[0].entry, ObjectIDs: []string{"o1"}, OK: false}})

	info2, ok := g.Tainted("o1")
	if !ok {
		t.Fatal("o1 must still be tainted")
	}
	if info2.EarliestStep != 1 {
		t.Fatalf("earliest step overwritten to %d; must remain 1", info2.EarliestStep)
	}
	if info2.Entry != info.Entry {
		t.Fatalf("entry overwritten %d -> %d; must retain first contamination entry", info.Entry, info2.Entry)
	}

	// 再用更早的步骤编号（0）卷入，同样不得覆盖。
	thirdStack := []*inverse{
		{step: 0, entry: g.nextEntry(), objects: []string{"o1"},
			apply: func() error { return nil }, failNow: true},
	}
	exec.compensate("third", thirdStack)
	g.commitTaints([]CompRecord{{Index: 0, Entry: thirdStack[0].entry, ObjectIDs: []string{"o1"}}})
	info3, _ := g.Tainted("o1")
	if info3.EarliestStep != 1 || info3.Entry != info.Entry {
		t.Fatalf("earliest contamination changed to %+v; must stay step 1 / entry %d",
			info3, info.Entry)
	}
}

// TestContentionRejectsBeforeAnyMutation 争用同一属性：
// 被拒方在任何可观察改动之前被拒绝，时钟戳/版本号不变。
func TestContentionRejectsBeforeAnyMutation(t *testing.T) {
	g, exec, _ := newHarness(t, 4)

	// 用一个长时间持锁动作模拟并发中的另一方：在 Check 中阻塞，
	// 此时它已持有全部守卫（生效尚未开始）。
	release := make(chan struct{})
	holderStarted := make(chan struct{})
	holder := &Action{ID: "holder", Ops: []SubOp{
		{
			Kind: OpSetProperties, ObjectID: "o0", Sets: map[string]any{"a": 1},
			Check: func() bool {
				close(holderStarted)
				<-release
				return true
			},
		},
	}}
	holderDone := make(chan *Outcome, 1)
	go func() { holderDone <- exec.Execute(context.Background(), holder) }()
	<-holderStarted

	verBefore, _ := g.ObjectVersion("o0")
	clockBefore, _ := g.ObjectClock("o0")

	// 竞争方：争同一对象的同一属性 a —— 必须争用拒绝。
	rival := &Action{ID: "rival", Ops: []SubOp{
		{Kind: OpSetProperties, ObjectID: "o0", Sets: map[string]any{"a": 2}},
	}}
	out := exec.Execute(context.Background(), rival)

	if out.Category != CategoryContention {
		t.Fatalf("want CONTENTION, got %s (%s)", out.Category, out.Reason)
	}
	if !out.RejectedBeforeEffect || len(out.Applied) != 0 {
		t.Fatal("rival must be rejected before any effect")
	}
	verAfter, _ := g.ObjectVersion("o0")
	clockAfter, _ := g.ObjectClock("o0")
	if verAfter != verBefore || clockAfter != clockBefore {
		t.Fatalf("invariants changed: version %d->%d clock %d->%d",
			verBefore, verAfter, clockBefore, clockAfter)
	}

	// 不相交属性（同对象不同 key）不算争用，应可并发通过。
	disjoint := &Action{ID: "disjoint", Ops: []SubOp{
		{Kind: OpSetProperties, ObjectID: "o0", Sets: map[string]any{"b": "y"}},
	}}
	out2 := exec.Execute(context.Background(), disjoint)
	if out2.Category != CategoryOK {
		t.Fatalf("disjoint property action must proceed, got %s", out2.Category)
	}

	close(release)
	h := <-holderDone
	if h.Category != CategoryOK {
		t.Fatalf("holder want OK, got %s", h.Category)
	}
	if v, _ := g.Prop("o0", "a"); v != 1 {
		t.Fatalf("o0.a = %v, want holder value 1", v)
	}
}

// TestCategoryPriority 固定判定优先级，与补偿是否发生无关：
// 污染 > 业务拒绝 > 争用 > 补偿失败。污染实例同时业务校验会失败时仍报污染。
func TestCategoryPriority(t *testing.T) {
	_, exec := poisonGraph(t)
	// 触达污染对象，且业务校验本应拒绝：必须报告 CONTAMINATED（更高优先级）。
	act := &Action{ID: "prio", Ops: []SubOp{
		{Kind: OpSetProperties, ObjectID: "o1", Sets: map[string]any{"a": 1},
			Check: func() bool { return false }},
	}}
	out := exec.Execute(context.Background(), act)
	if out.Category != CategoryContaminated {
		t.Fatalf("want CONTAMINATED to outrank business rejection, got %s", out.Category)
	}

	// 业务拒绝（步骤 1）与“本会发生的补偿失败”并存时，
	// 若补偿全部成功，类别保持 BUSINESS_REJECTED。
	_, exec2, _ := newHarness(t, 4)
	act2 := &Action{ID: "p2", Ops: []SubOp{
		{Kind: OpSetProperties, ObjectID: "o0", Sets: map[string]any{"a": 1}},
		{Check: func() bool { return false }},
	}}
	out2 := exec2.Execute(context.Background(), act2)
	if out2.Category != CategoryBusinessRejected {
		t.Fatalf("want BUSINESS_REJECTED with clean compensation, got %s", out2.Category)
	}

	// 编号顺序硬编码检查优先级关系。
	if !(CategoryContaminated > CategoryBusinessRejected &&
		CategoryBusinessRejected > CategoryContention &&
		CategoryContention > CategoryCompensationFailed &&
		CategoryCompensationFailed > CategoryOK) {
		t.Fatalf("category numeric priority broken: %d %d %d %d %d",
			CategoryContaminated, CategoryBusinessRejected,
			CategoryContention, CategoryCompensationFailed, CategoryOK)
	}
}

// TestGlobalEntryNumbersUnique 全局登记编号跨动作单调唯一，钩子也登记。
func TestGlobalEntryNumbersUnique(t *testing.T) {
	g, exec, _ := newHarness(t, 3)
	act := &Action{ID: "hooks", Ops: []SubOp{
		{Kind: OpHook, ObjectID: "o0", Hook: "validate"},
		{Kind: OpHook, ObjectID: "o1", Hook: "validate"},
	}}
	out := exec.Execute(context.Background(), act)
	if out.Category != CategoryOK {
		t.Fatalf("want OK, got %s", out.Category)
	}
	if out.Applied[0].Entry == 0 || out.Applied[1].Entry == 0 {
		t.Fatal("hook steps must also consume global entry numbers")
	}
	if out.Applied[0].Entry >= out.Applied[1].Entry {
		t.Fatalf("entries must be strictly increasing, got #%d #%d",
			out.Applied[0].Entry, out.Applied[1].Entry)
	}
	if c, _ := g.ObjectClock("o0"); c != 0 {
		t.Fatalf("hook must not bump clock, got %d", c)
	}
	fmt.Println("entry uniqueness ok")
}
