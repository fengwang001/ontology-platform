package ontology

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func mustEngine(t *testing.T, low, high int, timeout int64) *Engine {
	t.Helper()
	e, err := NewEngine(low, high, timeout)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func mustRule(t *testing.T, e *Engine, id, code string, score int) {
	t.Helper()
	if err := e.UpsertRule(Rule{ID: id, Code: code, Score: score}); err != nil {
		t.Fatalf("UpsertRule %s: %v", id, err)
	}
}

func mustReviewer(t *testing.T, e *Engine, id, branch string, level Level) {
	t.Helper()
	if err := e.RegisterReviewer(id, branch, level); err != nil {
		t.Fatalf("RegisterReviewer %s: %v", id, err)
	}
}

func mustAccept(t *testing.T, e *Engine, id string, at int64, feats []string, branch string, amount int64) {
	t.Helper()
	if err := e.Accept(id, at, feats, branch, amount); err != nil {
		t.Fatalf("Accept %s: %v", id, err)
	}
}

func mustAssign(t *testing.T, e *Engine, caseID string, slot int, rev string) {
	t.Helper()
	if err := e.Assign(caseID, slot, rev); err != nil {
		t.Fatalf("Assign %s[%d]=%s: %v", caseID, slot, rev, err)
	}
}

func mustSubmit(t *testing.T, e *Engine, caseID, rev string, v Verdict) {
	t.Helper()
	if err := e.Submit(caseID, rev, v); err != nil {
		t.Fatalf("Submit %s by %s: %v", caseID, rev, err)
	}
}

func mustAdvance(t *testing.T, e *Engine, now int64) {
	t.Helper()
	if err := e.Advance(now); err != nil {
		t.Fatalf("Advance %d: %v", now, err)
	}
}

func wantErr(t *testing.T, err error, sentinel error) {
	t.Helper()
	if !errors.Is(err, sentinel) {
		t.Fatalf("期望错误 %v，实际 %v", sentinel, err)
	}
}

func wantState(t *testing.T, e *Engine, caseID string, state CaseState) CaseSnapshot {
	t.Helper()
	snap, ok := e.Snapshot(caseID)
	if !ok {
		t.Fatalf("案件 %s 不存在", caseID)
	}
	if snap.State != state {
		t.Fatalf("案件 %s 期望状态 %s，实际 %s", caseID, state, snap.State)
	}
	return snap
}

// newStandardEngine 构造低阈值 10、高阈值 20、时限 100 的引擎，
// 规则：FA=10, FB=10, FC=5。
func newStandardEngine(t *testing.T) *Engine {
	t.Helper()
	e := mustEngine(t, 10, 20, 100)
	mustRule(t, e, "RA", "FA", 10)
	mustRule(t, e, "RB", "FB", 10)
	mustRule(t, e, "RC", "FC", 5)
	return e
}

// 总分恰等于低阈值进入单人复核，恰等于高阈值进入双人复核，
// 严格小于低阈值自动通过，任何总分都不直接自动拒付。
func TestRoutingThresholds(t *testing.T) {
	e := newStandardEngine(t)
	mustRule(t, e, "RD", "FD", 1000)

	mustAccept(t, e, "eq-low", 0, []string{"FA"}, "B1", 100)
	snap := wantState(t, e, "eq-low", StateWaiting)
	if snap.Score != 10 || len(snap.Slots) != 1 {
		t.Fatalf("eq-low: score=%d slots=%d", snap.Score, len(snap.Slots))
	}

	mustAccept(t, e, "eq-high", 0, []string{"FA", "FB"}, "B1", 100)
	snap = wantState(t, e, "eq-high", StateWaiting)
	if snap.Score != 20 || len(snap.Slots) != 2 {
		t.Fatalf("eq-high: score=%d slots=%d", snap.Score, len(snap.Slots))
	}

	mustAccept(t, e, "below", 0, []string{"FC"}, "B1", 100)
	wantState(t, e, "below", StateAutoPassed)

	mustAccept(t, e, "zero", 0, nil, "B1", 100)
	wantState(t, e, "zero", StateAutoPassed)

	mustAccept(t, e, "above", 0, []string{"FA", "FB", "FC"}, "B1", 100)
	wantState(t, e, "above", StateWaiting)

	mustAccept(t, e, "huge", 0, []string{"FD"}, "B1", 100)
	snap = wantState(t, e, "huge", StateWaiting)
	if len(snap.Slots) != 2 {
		t.Fatalf("huge: 任何总分不得直接自动拒付，slots=%d", len(snap.Slots))
	}
}

// 负分值使总分触底为零。
func TestNegativeScoreFloorZero(t *testing.T) {
	e := mustEngine(t, 10, 20, 100)
	mustRule(t, e, "RN", "FN", -50)
	mustRule(t, e, "RP", "FP", 5)

	mustAccept(t, e, "neg", 0, []string{"FN"}, "B1", 1)
	snap := wantState(t, e, "neg", StateAutoPassed)
	if snap.Score != 0 {
		t.Fatalf("neg: 总分应触底为零，实际 %d", snap.Score)
	}

	mustAccept(t, e, "mix", 0, []string{"FN", "FP"}, "B1", 1)
	snap = wantState(t, e, "mix", StateAutoPassed)
	if snap.Score != 0 {
		t.Fatalf("mix: 5-50 应触底为零，实际 %d", snap.Score)
	}
}

// 规则库在受理后变更不影响已固化分值。
func TestScoreSnapshotImmutable(t *testing.T) {
	e := newStandardEngine(t)
	mustAccept(t, e, "c1", 0, []string{"FA"}, "B1", 1)

	mustRule(t, e, "RA", "FA", 999)
	if err := e.DeleteRule("RB"); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}

	snap := wantState(t, e, "c1", StateWaiting)
	if snap.Score != 10 {
		t.Fatalf("c1: 受理后规则库变更不得影响分值，实际 %d", snap.Score)
	}

	mustAccept(t, e, "c2", 1, []string{"FA", "FB"}, "B1", 1)
	snap = wantState(t, e, "c2", StateWaiting)
	if snap.Score != 999 {
		t.Fatalf("c2: 应适用新规则库，实际 %d", snap.Score)
	}
}

// newDualCase 构造一个双人复核案件并登记三名复核员：
// r1（一级）、r2（二级）、r3（二级），均与案件不同网点。
func newDualCase(t *testing.T) (*Engine, string) {
	t.Helper()
	e := newStandardEngine(t)
	mustReviewer(t, e, "r1", "B2", LevelOne)
	mustReviewer(t, e, "r2", "B2", LevelTwo)
	mustReviewer(t, e, "r3", "B3", LevelTwo)
	mustAccept(t, e, "dual", 0, []string{"FA", "FB"}, "B1", 1)
	return e, "dual"
}

// 第二名复核员必须为二级且与第一名不同人；空位须按序分配。
func TestDualSlotConstraints(t *testing.T) {
	e, c := newDualCase(t)

	wantErr(t, e.Assign(c, 1, "r2"), ErrInvalidParam) // 跳过空位 0
	mustAssign(t, e, c, 0, "r2")                      // 第一名可以是二级
	wantErr(t, e.Assign(c, 1, "r2"), ErrInvalidParam) // 与第一名同人
	wantErr(t, e.Assign(c, 1, "r1"), ErrInvalidParam) // 第二名须为二级
	mustAssign(t, e, c, 1, "r3")
	wantState(t, e, c, StateInReview)
}

// 复核员与案件报案网点相同视为利益冲突。
func TestConflict(t *testing.T) {
	e := newStandardEngine(t)
	mustReviewer(t, e, "same", "B1", LevelOne)
	mustReviewer(t, e, "other", "B2", LevelOne)
	mustAccept(t, e, "c1", 0, []string{"FA"}, "B1", 1)

	wantErr(t, e.Assign("c1", 0, "same"), ErrConflict)
	if _, _, err := e.ApplyAssign("same"); !errors.Is(err, ErrConflict) {
		t.Fatalf("ApplyAssign 期望利益冲突，实际 %v", err)
	}
	mustAssign(t, e, "c1", 0, "other")
}

// 重复分配同一空位报「已分配」。
func TestSlotTaken(t *testing.T) {
	e := newStandardEngine(t)
	mustReviewer(t, e, "r1", "B2", LevelOne)
	mustReviewer(t, e, "r2", "B3", LevelOne)
	mustAccept(t, e, "c1", 0, []string{"FA"}, "B1", 1)

	mustAssign(t, e, "c1", 0, "r1")
	wantErr(t, e.Assign("c1", 0, "r2"), ErrSlotTaken)
}

// 恰在到期时刻提交的人工结论被拒绝并报「已超时」，案件按自动通过处理。
func TestSubmitAtExactDeadline(t *testing.T) {
	e := newStandardEngine(t)
	mustReviewer(t, e, "r1", "B2", LevelOne)
	mustAccept(t, e, "c1", 0, []string{"FA"}, "B1", 1)
	mustAssign(t, e, "c1", 0, "r1") // 分配时刻 0，时限 100

	mustAdvance(t, e, 100) // 恰在到期时刻
	wantErr(t, e.Submit("c1", "r1", VerdictReject), ErrTimeout)

	snap := wantState(t, e, "c1", StatePassed)
	if !snap.Slots[0].Auto || snap.Slots[0].Verdict != VerdictPass {
		t.Fatalf("c1: 空位应按通过自动填入，实际 %+v", snap.Slots[0])
	}
}

// 单人复核超时自动通过。
func TestSingleTimeoutAutoPass(t *testing.T) {
	e := newStandardEngine(t)
	mustReviewer(t, e, "r1", "B2", LevelOne)
	mustAccept(t, e, "c1", 0, []string{"FA"}, "B1", 1)
	mustAssign(t, e, "c1", 0, "r1")

	mustAdvance(t, e, 150)
	snap := wantState(t, e, "c1", StatePassed)
	if !snap.Slots[0].Auto {
		t.Fatalf("c1: 应为超时自动通过，实际 %+v", snap.Slots[0])
	}
	if e.PendingCount() != 0 {
		t.Fatalf("终态案件不得留在等待队列，pending=%d", e.PendingCount())
	}
}

// 双人复核一人超时视为通过、另一人拒付，进入仲裁；仲裁结论即案件结论。
func TestDualTimeoutVsRejectGoesArbitration(t *testing.T) {
	e, c := newDualCase(t)
	mustAssign(t, e, c, 0, "r1")
	mustAssign(t, e, c, 1, "r2")
	mustSubmit(t, e, c, "r2", VerdictReject)

	mustAdvance(t, e, 100) // r1 超时自动通过
	snap := wantState(t, e, c, StateWaiting)
	if len(snap.Slots) != 3 {
		t.Fatalf("一人通过一人拒付应进入仲裁，slots=%d", len(snap.Slots))
	}
	if !snap.Slots[0].Auto || snap.Slots[0].Verdict != VerdictPass {
		t.Fatalf("空位 0 应为超时自动通过，实际 %+v", snap.Slots[0])
	}

	mustAssign(t, e, c, 2, "r3")
	mustSubmit(t, e, c, "r3", VerdictReject)
	wantState(t, e, c, StateRejected)
}

// 仲裁空位超时同样按通过处理。
func TestArbitrationTimeout(t *testing.T) {
	e, c := newDualCase(t)
	mustAssign(t, e, c, 0, "r1")
	mustAssign(t, e, c, 1, "r2")
	mustSubmit(t, e, c, "r1", VerdictPass)
	mustSubmit(t, e, c, "r2", VerdictReject) // 不一致，进入仲裁

	mustAssign(t, e, c, 2, "r3")
	mustAdvance(t, e, 100) // 仲裁员超时自动通过
	snap := wantState(t, e, c, StatePassed)
	if !snap.Slots[2].Auto || snap.Slots[2].Verdict != VerdictPass {
		t.Fatalf("仲裁空位应按通过自动填入，实际 %+v", snap.Slots[2])
	}
}

// 仲裁员须为二级、不同于前两人且无利益冲突。
func TestArbiterConstraints(t *testing.T) {
	e, c := newDualCase(t)
	mustReviewer(t, e, "r4", "B1", LevelTwo) // 与案件同网点
	mustAssign(t, e, c, 0, "r1")
	mustAssign(t, e, c, 1, "r2")
	mustSubmit(t, e, c, "r1", VerdictPass)
	mustSubmit(t, e, c, "r2", VerdictReject)

	wantErr(t, e.Assign(c, 2, "r1"), ErrInvalidParam) // 一级且为第一名
	wantErr(t, e.Assign(c, 2, "r2"), ErrInvalidParam) // 与第二名同人
	wantErr(t, e.Assign(c, 2, "r4"), ErrConflict)     // 利益冲突
	mustAssign(t, e, c, 2, "r3")
}

// 撤回为终态且与通过、拒付可区分；撤回后迟到的结论报「已终态」。
func TestWithdraw(t *testing.T) {
	e := newStandardEngine(t)
	mustReviewer(t, e, "r1", "B2", LevelOne)
	mustAccept(t, e, "c1", 0, []string{"FA"}, "B1", 1)
	mustAccept(t, e, "c2", 1, []string{"FA"}, "B1", 1)
	mustAssign(t, e, "c1", 0, "r1")

	if err := e.Withdraw("c1"); err != nil {
		t.Fatalf("Withdraw c1: %v", err)
	}
	snap := wantState(t, e, "c1", StateWithdrawn)
	if snap.State == StatePassed || snap.State == StateRejected {
		t.Fatalf("撤回必须与通过、拒付可区分")
	}
	if !snap.Slots[0].Void {
		t.Fatalf("撤回后未提交空位应作废，实际 %+v", snap.Slots[0])
	}
	wantErr(t, e.Submit("c1", "r1", VerdictPass), ErrTerminal) // 迟到的结论
	wantErr(t, e.Withdraw("c1"), ErrTerminal)                  // 重复撤回
	wantErr(t, e.Assign("c1", 0, "r1"), ErrTerminal)           // 终态后分配

	if e.PendingCount() != 1 {
		t.Fatalf("c1 撤回后应出队，pending=%d", e.PendingCount())
	}
	if err := e.Withdraw("c2"); err != nil { // 等待中的案件也可撤回
		t.Fatalf("Withdraw c2: %v", err)
	}
	wantState(t, e, "c2", StateWithdrawn)
	if e.PendingCount() != 0 {
		t.Fatalf("c2 撤回后应出队，pending=%d", e.PendingCount())
	}
}

// 已终态案件（含自动通过）不可撤回。
func TestWithdrawTerminal(t *testing.T) {
	e := newStandardEngine(t)
	mustAccept(t, e, "auto", 0, []string{"FC"}, "B1", 1)
	wantErr(t, e.Withdraw("auto"), ErrTerminal)
}

// 拒绝次序逐对验证：参数非法 > 时钟回退 > 案件不存在 > 复核员不存在 >
// 已终态 > 已超时 > 非本人 > 利益冲突 > 已分配 > 无待办。
func TestRejectionOrder(t *testing.T) {
	e := newStandardEngine(t)
	mustReviewer(t, e, "r1", "B2", LevelOne)
	mustReviewer(t, e, "r2", "B3", LevelOne)
	mustReviewer(t, e, "conflict", "B1", LevelOne)
	mustAccept(t, e, "auto", 0, []string{"FC"}, "B1", 1) // 自动通过（终态）
	mustAccept(t, e, "single", 1, []string{"FA"}, "B1", 1)
	mustAccept(t, e, "dual", 2, []string{"FA", "FB"}, "B1", 1)

	// 参数非法 > 时钟回退：负时刻既非法又回退
	mustAdvance(t, e, 50)
	wantErr(t, e.Advance(-1), ErrInvalidParam)
	wantErr(t, e.Advance(49), ErrClockRewind)

	// 参数非法 > 案件不存在：非法结论 + 不存在案件
	wantErr(t, e.Submit("ghost", "r1", VerdictNone), ErrInvalidParam)

	// 案件不存在 > 复核员不存在
	wantErr(t, e.Submit("ghost", "ghostRev", VerdictPass), ErrCaseNotFound)
	wantErr(t, e.Assign("ghost", 0, "ghostRev"), ErrCaseNotFound)

	// 复核员不存在 > 已终态
	wantErr(t, e.Assign("auto", 0, "ghostRev"), ErrReviewerNotFound)

	// 已终态 > 利益冲突
	wantErr(t, e.Assign("auto", 0, "conflict"), ErrTerminal)

	// 已终态 > 已超时：single 空位超时后案件终态，无关者提交报已终态
	mustAssign(t, e, "single", 0, "r1")
	mustAdvance(t, e, 200) // single 空位到期自动通过，案件终态
	wantErr(t, e.Submit("single", "r2", VerdictPass), ErrTerminal)

	// 已超时 > 非本人：本人空位被自动填入后，本人提交报已超时
	wantErr(t, e.Submit("single", "r1", VerdictReject), ErrTimeout)

	// 利益冲突 > 已分配：空位 0 已分配给 r1，冲突者再分配报利益冲突
	mustAssign(t, e, "dual", 0, "r1")
	wantErr(t, e.Assign("dual", 0, "conflict"), ErrConflict)

	// 已分配
	wantErr(t, e.Assign("dual", 0, "r2"), ErrSlotTaken)

	// 复核员不存在 > 无待办
	e2 := newStandardEngine(t)
	if _, _, err := e2.ApplyAssign("ghostRev"); !errors.Is(err, ErrReviewerNotFound) {
		t.Fatalf("期望复核员不存在，实际 %v", err)
	}

	// 无待办
	mustReviewer(t, e2, "rx", "B9", LevelOne)
	if _, _, err := e2.ApplyAssign("rx"); !errors.Is(err, ErrNoPending) {
		t.Fatalf("期望无待办，实际 %v", err)
	}
}

// 各类错误可区分且为基本校验。
func TestBasicErrors(t *testing.T) {
	e := newStandardEngine(t)
	mustReviewer(t, e, "r1", "B2", LevelOne)
	mustAccept(t, e, "c1", 0, []string{"FA"}, "B1", 1)

	wantErr(t, e.Accept("c1", 0, nil, "B1", 1), ErrInvalidParam)          // 案件号重复
	wantErr(t, e.Accept("c2", -1, nil, "B1", 1), ErrInvalidParam)         // 受理时刻为负
	wantErr(t, e.Accept("c3", 0, nil, "B1", -1), ErrInvalidParam)         // 金额为负
	wantErr(t, e.RegisterReviewer("r1", "B2", LevelOne), ErrInvalidParam) // 复核员重复
	wantErr(t, e.RegisterReviewer("r9", "B2", Level(3)), ErrInvalidParam) // 级别非法
	wantErr(t, e.Submit("c1", "r1", VerdictPass), ErrNotAssignee)         // 空位未分配
	mustAssign(t, e, "c1", 0, "r1")
	wantErr(t, e.Submit("c1", "r1", VerdictNone), ErrInvalidParam) // 结论非法
	if _, err := NewEngine(20, 10, 100); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("低阈值须严格小于高阈值，实际 %v", err)
	}
	if _, err := NewEngine(10, 20, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("时限须为正，实际 %v", err)
	}
}

// 被拒绝的操作不得改变案件状态、分配关系与当前时刻。
func TestRejectedOpKeepsState(t *testing.T) {
	e := newStandardEngine(t)
	mustReviewer(t, e, "r1", "B2", LevelOne)
	mustReviewer(t, e, "r2", "B3", LevelOne)
	mustReviewer(t, e, "conflict", "B1", LevelOne)
	mustAccept(t, e, "c1", 0, []string{"FA"}, "B1", 1)
	mustAccept(t, e, "c2", 1, []string{"FA", "FB"}, "B1", 1)
	mustAssign(t, e, "c1", 0, "r1")
	mustAdvance(t, e, 30)

	before := dumpAll(e, []string{"c1", "c2"})

	rejected := []error{
		e.Submit("c1", "r1", VerdictNone),    // 参数非法
		e.Advance(29),                        // 时钟回退
		e.Submit("ghost", "r1", VerdictPass), // 案件不存在
		e.Assign("c2", 0, "ghostRev"),        // 复核员不存在
		e.Submit("c1", "r2", VerdictPass),    // 非本人
		e.Assign("c2", 0, "conflict"),        // 利益冲突
		e.Assign("c1", 0, "r2"),              // 已分配
		e.Withdraw("ghost"),                  // 案件不存在
		e.Accept("c1", 5, nil, "B1", 1),      // 参数非法（重复）
	}
	for i, err := range rejected {
		if err == nil {
			t.Fatalf("第 %d 个操作应被拒绝", i)
		}
	}
	if _, _, err := e.ApplyAssign("ghostRev"); err == nil { // 复核员不存在
		t.Fatalf("ApplyAssign 应被拒绝")
	}

	after := dumpAll(e, []string{"c1", "c2"})
	if before != after {
		t.Fatalf("被拒操作留下痕迹：\n前：\n%s\n后：\n%s", before, after)
	}
}

func dumpAll(e *Engine, ids []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "now=%d pending=%d\n", e.Now(), e.PendingCount())
	for _, id := range ids {
		snap, ok := e.Snapshot(id)
		if !ok {
			fmt.Fprintf(&b, "%s <不存在>\n", id)
			continue
		}
		fmt.Fprintf(&b, "%s score=%d state=%s slots=%+v\n", id, snap.Score, snap.State, snap.Slots)
	}
	return b.String()
}

// 两名复核员对同一双人案件并发提交结论，最终结果必须与某种
// 先后顺序下依次提交一致。
func TestConcurrentSubmitDual(t *testing.T) {
	for trial := 0; trial < 50; trial++ {
		e, c := newDualCase(t)
		mustAssign(t, e, c, 0, "r1")
		mustAssign(t, e, c, 1, "r2")

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _ = e.Submit(c, "r1", VerdictPass) }()
		go func() { defer wg.Done(); _ = e.Submit(c, "r2", VerdictReject) }()
		wg.Wait()

		// 两人结论不一致，无论谁先谁后都必须进入仲裁，
		// 不得出现仲裁与直接结论并存。
		snap := wantState(t, e, c, StateWaiting)
		if len(snap.Slots) != 3 {
			t.Fatalf("并发不一致结论应进入仲裁，slots=%d", len(snap.Slots))
		}
	}
}

// 并发提交相同结论应直接终态。
func TestConcurrentSubmitAgree(t *testing.T) {
	for trial := 0; trial < 50; trial++ {
		e, c := newDualCase(t)
		mustAssign(t, e, c, 0, "r1")
		mustAssign(t, e, c, 1, "r2")

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _ = e.Submit(c, "r1", VerdictPass) }()
		go func() { defer wg.Done(); _ = e.Submit(c, "r2", VerdictPass) }()
		wg.Wait()
		wantState(t, e, c, StatePassed)
	}
}

// 并发申请分配：每个案件空位至多被分配一次。
func TestConcurrentApplyAssign(t *testing.T) {
	e := mustEngine(t, 10, 20, 1000)
	mustRule(t, e, "RA", "FA", 10)
	const n = 16
	for i := 0; i < n; i++ {
		mustReviewer(t, e, fmt.Sprintf("rev%d", i), "B2", LevelOne)
		mustAccept(t, e, fmt.Sprintf("case%d", i), int64(i), []string{"FA"}, "B1", 1)
	}

	results := make([]string, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			caseID, _, err := e.ApplyAssign(fmt.Sprintf("rev%d", i))
			if err != nil {
				t.Errorf("ApplyAssign: %v", err)
				return
			}
			results[i] = caseID
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool)
	for _, caseID := range results {
		if caseID == "" {
			t.Fatalf("存在未成功的申请")
		}
		if seen[caseID] {
			t.Fatalf("案件 %s 被重复分配", caseID)
		}
		seen[caseID] = true
	}
	if e.PendingCount() != 0 {
		t.Fatalf("全部分配完毕，pending=%d", e.PendingCount())
	}
	if _, _, err := e.ApplyAssign("rev0"); !errors.Is(err, ErrNoPending) {
		t.Fatalf("期望无待办，实际 %v", err)
	}
}

// 并发提交与撤回：最终状态必为通过或已撤回之一，且与某串行序一致。
func TestConcurrentSubmitVsWithdraw(t *testing.T) {
	for trial := 0; trial < 50; trial++ {
		e := newStandardEngine(t)
		mustReviewer(t, e, "r1", "B2", LevelOne)
		mustAccept(t, e, "c1", 0, []string{"FA"}, "B1", 1)
		mustAssign(t, e, "c1", 0, "r1")

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _ = e.Submit("c1", "r1", VerdictPass) }()
		go func() { defer wg.Done(); _ = e.Withdraw("c1") }()
		wg.Wait()

		snap, _ := e.Snapshot("c1")
		if snap.State != StatePassed && snap.State != StateWithdrawn {
			t.Fatalf("终态必须为通过或已撤回，实际 %s", snap.State)
		}
		wantErr(t, e.Submit("c1", "r1", VerdictPass), ErrTerminal)
	}
}

// 相同操作序列重放得到完全相同的分流、分配与结论。
func TestDeterministicReplay(t *testing.T) {
	run := func() string {
		e := newStandardEngine(t)
		mustReviewer(t, e, "r1", "B2", LevelOne)
		mustReviewer(t, e, "r2", "B2", LevelTwo)
		mustReviewer(t, e, "r3", "B3", LevelTwo)
		mustAccept(t, e, "k1", 5, []string{"FA", "FB"}, "B1", 1)
		mustAccept(t, e, "k2", 5, []string{"FA"}, "B1", 1) // 与 k1 同时刻，按受理先后
		mustAccept(t, e, "k3", 3, []string{"FA"}, "B1", 1)
		mustAdvance(t, e, 10)

		// 申请分配应取受理时刻最早的 k3，其次同时刻先受理的 k1。
		id1, _, err := e.ApplyAssign("r1")
		if err != nil || id1 != "k3" {
			t.Fatalf("首次申请应分得 k3，实际 %s %v", id1, err)
		}
		id2, _, err := e.ApplyAssign("r2")
		if err != nil || id2 != "k1" {
			t.Fatalf("二次申请应分得 k1，实际 %s %v", id2, err)
		}
		mustSubmit(t, e, "k3", "r1", VerdictReject)
		mustAdvance(t, e, 200)
		return dumpAll(e, []string{"k1", "k2", "k3"})
	}
	first, second := run(), run()
	if first != second {
		t.Fatalf("重放结果不一致：\n%s\n%s", first, second)
	}
}
