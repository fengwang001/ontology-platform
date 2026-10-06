package claims

import (
	"reflect"
	"sync"
	"testing"
)

// newTestEngine 构造测试引擎：低阈值 2、高阈值 5、空位时限 100 秒。
// 规则：FA=+2 FB=+3 FC=+5 FN=-4；复核员 r1(一级) r2/r3(二级) 属 b0，r4(二级) 属 b1。
func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := NewEngine(Config{LowThreshold: 2, HighThreshold: 5, ReviewTimeout: 100})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	for _, r := range []Rule{
		{ID: "R1", Feature: "FA", Score: 2},
		{ID: "R2", Feature: "FB", Score: 3},
		{ID: "R3", Feature: "FC", Score: 5},
		{ID: "R4", Feature: "FN", Score: -4},
	} {
		if err := e.SetRule(r.ID, r.Feature, r.Score); err != nil {
			t.Fatalf("SetRule: %v", err)
		}
	}
	for _, rv := range []Reviewer{
		{ID: "r1", Branch: "b0", Level: LevelOne},
		{ID: "r2", Branch: "b0", Level: LevelTwo},
		{ID: "r3", Branch: "b0", Level: LevelTwo},
		{ID: "r4", Branch: "b1", Level: LevelTwo},
	} {
		if err := e.RegisterReviewer(rv.ID, rv.Branch, rv.Level); err != nil {
			t.Fatalf("RegisterReviewer: %v", err)
		}
	}
	return e
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if got != want {
		t.Fatalf("err = %v, want %v", got, want)
	}
}

func mustAccept(t *testing.T, e *Engine, id string, at int64, feats []string, branch string) {
	t.Helper()
	if err := e.AcceptCase(id, at, feats, branch, 100); err != nil {
		t.Fatalf("AcceptCase(%s): %v", id, err)
	}
}

func mustAssign(t *testing.T, e *Engine, caseID string, slotIdx int, reviewer string) {
	t.Helper()
	if err := e.Assign(caseID, slotIdx, reviewer); err != nil {
		t.Fatalf("Assign(%s,%d,%s): %v", caseID, slotIdx, reviewer, err)
	}
}

func mustSubmit(t *testing.T, e *Engine, caseID string, slotIdx int, reviewer string, concl Conclusion) {
	t.Helper()
	if err := e.Submit(caseID, slotIdx, reviewer, concl); err != nil {
		t.Fatalf("Submit(%s,%d,%s): %v", caseID, slotIdx, reviewer, err)
	}
}

func mustAdvance(t *testing.T, e *Engine, now int64) {
	t.Helper()
	if err := e.Advance(now); err != nil {
		t.Fatalf("Advance(%d): %v", now, err)
	}
}

func snapshot(t *testing.T, e *Engine, caseID string) CaseSnapshot {
	t.Helper()
	s, err := e.Snapshot(caseID)
	if err != nil {
		t.Fatalf("Snapshot(%s): %v", caseID, err)
	}
	return s
}

// 总分恰等于低阈值进入单人复核，恰等于高阈值进入双人复核；
// 严格小于低阈值自动通过；任何总分都不直接自动拒付。
func TestRoutingThresholds(t *testing.T) {
	e := newTestEngine(t)
	mustAccept(t, e, "below", 0, nil, "b1")                    // 0 < 2：自动通过
	mustAccept(t, e, "at-low", 1, []string{"FA"}, "b1")        // 2：单人复核
	mustAccept(t, e, "mid", 2, []string{"FB"}, "b1")           // 3：单人复核
	mustAccept(t, e, "at-high", 3, []string{"FA", "FB"}, "b1") // 5：双人复核
	mustAccept(t, e, "above", 4, []string{"FC", "FA"}, "b1")   // 7：双人复核

	if s := snapshot(t, e, "below"); s.Outcome != OutcomePass || len(s.Slots) != 0 {
		t.Fatalf("below: %+v", s)
	}
	for _, id := range []string{"at-low", "mid"} {
		if s := snapshot(t, e, id); s.Outcome != OutcomeNone || len(s.Slots) != 1 {
			t.Fatalf("%s: %+v", id, s)
		}
	}
	for _, id := range []string{"at-high", "above"} {
		if s := snapshot(t, e, id); s.Outcome != OutcomeNone || len(s.Slots) != 2 {
			t.Fatalf("%s: %+v", id, s)
		}
	}
	// 高总分案件也必须经复核得出结论，不存在自动拒付。
	mustAssign(t, e, "above", 0, "r1")
	mustAssign(t, e, "above", 1, "r2")
	mustSubmit(t, e, "above", 0, "r1", ConclusionReject)
	mustSubmit(t, e, "above", 1, "r2", ConclusionReject)
	if s := snapshot(t, e, "above"); s.Outcome != OutcomeReject {
		t.Fatalf("above: %+v", s)
	}
}

// 负分值使总分触底为零：触发 FN(-4) 与 FA(+2) 合计 -2，固化总分为 0。
func TestNegativeScoreFloorZero(t *testing.T) {
	e := newTestEngine(t)
	mustAccept(t, e, "neg", 0, []string{"FA", "FN"}, "b1")
	s := snapshot(t, e, "neg")
	if s.Score != 0 || s.Outcome != OutcomePass {
		t.Fatalf("neg: %+v", s)
	}
}

// 规则库在受理后变更不影响已固化分值。
func TestRuleChangeAfterAccept(t *testing.T) {
	e := newTestEngine(t)
	mustAccept(t, e, "frozen", 0, []string{"FB"}, "b1") // 3 分：单人复核
	if err := e.SetRule("R2", "FB", 50); err != nil {
		t.Fatalf("SetRule: %v", err)
	}
	if err := e.RemoveRule("R1"); err != nil {
		t.Fatalf("RemoveRule: %v", err)
	}
	s := snapshot(t, e, "frozen")
	if s.Score != 3 || len(s.Slots) != 1 {
		t.Fatalf("frozen: %+v", s)
	}
	// 新受理的案件适用变更后的规则库。
	mustAccept(t, e, "fresh", 1, []string{"FB"}, "b1") // 50 分：双人复核
	if s := snapshot(t, e, "fresh"); s.Score != 50 || len(s.Slots) != 2 {
		t.Fatalf("fresh: %+v", s)
	}
}

// 双人复核第二名必须为二级且与第一名不同人。
func TestDoubleReviewSecondSlotChecks(t *testing.T) {
	e := newTestEngine(t)
	mustAccept(t, e, "dbl", 0, []string{"FC"}, "b1") // 5 分：双人复核
	mustAssign(t, e, "dbl", 0, "r1")                 // 第一名可为一级
	wantErr(t, e.Assign("dbl", 1, "r1"), ErrSameReviewer)
	// 一级复核员不能担任第二名：换 r1 之外的一级复核员。
	if err := e.RegisterReviewer("r1b", "b0", LevelOne); err != nil {
		t.Fatalf("RegisterReviewer: %v", err)
	}
	wantErr(t, e.Assign("dbl", 1, "r1b"), ErrLevelMismatch)
	mustAssign(t, e, "dbl", 1, "r2")
	// 两人结论一致即为案件结论。
	mustSubmit(t, e, "dbl", 0, "r1", ConclusionPass)
	mustSubmit(t, e, "dbl", 1, "r2", ConclusionPass)
	if s := snapshot(t, e, "dbl"); s.Outcome != OutcomePass {
		t.Fatalf("dbl: %+v", s)
	}
}

// 复核员与案件报案网点相同视为利益冲突。
func TestConflictOfInterest(t *testing.T) {
	e := newTestEngine(t)
	mustAccept(t, e, "c1", 0, []string{"FA"}, "b0") // 报案网点 b0
	wantErr(t, e.Assign("c1", 0, "r1"), ErrConflict)
	_, _, err := e.RequestAssign("r2")
	wantErr(t, err, ErrConflict)
	// 不同网点复核员可以分配。
	mustAssign(t, e, "c1", 0, "r4")
}

// 恰在到期时刻提交的人工结论被拒绝并报“已超时”，案件按自动通过处理。
func TestSubmitExactlyAtDeadline(t *testing.T) {
	e := newTestEngine(t)
	mustAccept(t, e, "s1", 0, []string{"FB"}, "b1") // 3 分：单人复核
	mustAssign(t, e, "s1", 0, "r1")                 // 分配时刻 0，到期时刻 100
	mustAdvance(t, e, 100)                          // 恰在到期时刻
	wantErr(t, e.Submit("s1", 0, "r1", ConclusionReject), ErrSlotTimeout)
	s := snapshot(t, e, "s1")
	if s.Outcome != OutcomePass || !s.Slots[0].Auto || s.Slots[0].Conclusion != ConclusionPass {
		t.Fatalf("s1: %+v", s)
	}
}

// 单人复核超时自动通过；到期前一刻不超时。
func TestSingleReviewTimeoutAutoPass(t *testing.T) {
	e := newTestEngine(t)
	mustAccept(t, e, "s2", 0, []string{"FB"}, "b1")
	mustAssign(t, e, "s2", 0, "r1")
	mustAdvance(t, e, 99)
	if s := snapshot(t, e, "s2"); s.Outcome != OutcomeNone {
		t.Fatalf("s2 @99: %+v", s)
	}
	mustAdvance(t, e, 100)
	s := snapshot(t, e, "s2")
	if s.Outcome != OutcomePass || !s.Slots[0].Auto {
		t.Fatalf("s2 @100: %+v", s)
	}
}

// 双人复核一人超时视为通过、另一人拒付，进入仲裁；仲裁结论即案件结论。
func TestDoubleTimeoutVsRejectGoesArbitration(t *testing.T) {
	e := newTestEngine(t)
	mustAccept(t, e, "d2", 0, []string{"FC"}, "b1")
	mustAssign(t, e, "d2", 0, "r1") // 到期时刻 100
	mustAdvance(t, e, 50)
	mustAssign(t, e, "d2", 1, "r2") // 到期时刻 150
	mustAdvance(t, e, 100)          // 空位 0 超时
	mustSubmit(t, e, "d2", 1, "r2", ConclusionReject)
	s := snapshot(t, e, "d2")
	if s.Outcome != OutcomeNone || len(s.Slots) != 3 {
		t.Fatalf("d2 arbitration: %+v", s)
	}
	if !s.Slots[0].Auto || s.Slots[0].Conclusion != ConclusionPass {
		t.Fatalf("d2 slot0: %+v", s.Slots[0])
	}
	// 仲裁员须为二级、不同于前两人。
	wantErr(t, e.Assign("d2", 2, "r1"), ErrSameReviewer)
	mustAssign(t, e, "d2", 2, "r3")
	mustSubmit(t, e, "d2", 2, "r3", ConclusionReject)
	if s := snapshot(t, e, "d2"); s.Outcome != OutcomeReject {
		t.Fatalf("d2 final: %+v", s)
	}
}

// 仲裁空位超时同样按通过处理。
func TestArbiterTimeout(t *testing.T) {
	e := newTestEngine(t)
	mustAccept(t, e, "d3", 0, []string{"FC"}, "b1")
	mustAssign(t, e, "d3", 0, "r1")
	mustAssign(t, e, "d3", 1, "r2")
	mustSubmit(t, e, "d3", 0, "r1", ConclusionPass)
	mustSubmit(t, e, "d3", 1, "r2", ConclusionReject) // 不一致，进入仲裁
	mustAssign(t, e, "d3", 2, "r3")                   // 分配时刻 0，到期时刻 100
	mustAdvance(t, e, 100)
	s := snapshot(t, e, "d3")
	if s.Outcome != OutcomePass || !s.Slots[2].Auto {
		t.Fatalf("d3: %+v", s)
	}
}

// 撤回为终态且与通过、拒付可区分；撤回后迟到的结论报“已终态”。
func TestWithdraw(t *testing.T) {
	e := newTestEngine(t)
	mustAccept(t, e, "w1", 0, []string{"FB"}, "b1")
	mustAssign(t, e, "w1", 0, "r1")
	if err := e.Withdraw("w1"); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if s := snapshot(t, e, "w1"); s.Outcome != OutcomeWithdrawn {
		t.Fatalf("w1: %+v", s)
	}
	wantErr(t, e.Submit("w1", 0, "r1", ConclusionPass), ErrCaseFinal)
	wantErr(t, e.Withdraw("w1"), ErrCaseFinal)
	// 已终态（自动通过）案件不可撤回。
	mustAccept(t, e, "w2", 1, nil, "b1")
	wantErr(t, e.Withdraw("w2"), ErrCaseFinal)
}

// 拒绝次序逐对验证：参数非法 > 时钟回退 > 案件不存在 > 复核员不存在 >
// 已终态 > 已超时 > 非本人 > 利益冲突 > 已分配 > 无待办。
func TestRejectionOrderPairs(t *testing.T) {
	e := newTestEngine(t)
	mustAccept(t, e, "base", 10, []string{"FC"}, "b1") // 双人复核，当前时刻 10
	mustAssign(t, e, "base", 0, "r1")
	mustAssign(t, e, "base", 1, "r2")
	mustAccept(t, e, "fin", 20, nil, "b1") // 自动通过，已终态
	mustAccept(t, e, "wd", 30, []string{"FA"}, "b1")
	mustAssign(t, e, "wd", 0, "r1")
	if err := e.Withdraw("wd"); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	mustAdvance(t, e, 200) // base 两空位与 wd 空位均已超时

	// 参数非法 > 时钟回退：空案件号且受理时刻回退。
	wantErr(t, e.AcceptCase("", 0, nil, "b1", 1), ErrInvalidParam)
	// 时钟回退：参数合法但时刻回退。
	wantErr(t, e.AcceptCase("new", 5, nil, "b1", 1), ErrClockRollback)
	wantErr(t, e.Advance(100), ErrClockRollback)
	// 案件不存在 > 复核员不存在。
	wantErr(t, e.Submit("ghost", 0, "ghost", ConclusionPass), ErrCaseNotFound)
	// 复核员不存在 > 已终态。
	wantErr(t, e.Submit("fin", 0, "ghost", ConclusionPass), ErrReviewerNotFound)
	// 已终态 > 已超时：撤回案件的空位虽已超时，仍报已终态。
	wantErr(t, e.Submit("wd", 0, "r1", ConclusionPass), ErrCaseFinal)
	// 已超时 > 非本人：空位 0 属 r1 且已超时，r2 提交仍报已超时。
	wantErr(t, e.Submit("base", 0, "r2", ConclusionPass), ErrSlotTimeout)
	// 非本人：未超时案件上由他人提交。
	mustAccept(t, e, "solo", 200, []string{"FA"}, "b1")
	mustAssign(t, e, "solo", 0, "r1")
	wantErr(t, e.Submit("solo", 0, "r2", ConclusionPass), ErrNotAssignee)
	// 利益冲突 > 已分配：报案网点 b0 的案件，冲突复核员优先报利益冲突。
	mustAccept(t, e, "cf", 210, []string{"FA"}, "b0")
	mustAssign(t, e, "cf", 0, "r4")
	wantErr(t, e.Assign("cf", 0, "r1"), ErrConflict)
	// 已分配：无冲突复核员分配到已占用空位。
	if err := e.RegisterReviewer("r5", "b2", LevelOne); err != nil {
		t.Fatalf("RegisterReviewer: %v", err)
	}
	wantErr(t, e.Assign("cf", 0, "r5"), ErrSlotOccupied)
	// 复核员不存在 > 无待办。
	_, _, err := e.RequestAssign("ghost")
	wantErr(t, err, ErrReviewerNotFound)
	// 无待办：清空待分配队列后。
	e2 := newTestEngine(t)
	_, _, err = e2.RequestAssign("r1")
	wantErr(t, err, ErrNoPending)
}

// 被拒绝的操作不得改变案件状态、分配关系与当前时刻。
func TestRejectedOpLeavesNoTrace(t *testing.T) {
	e := newTestEngine(t)
	mustAccept(t, e, "k1", 0, []string{"FC"}, "b1")
	mustAssign(t, e, "k1", 0, "r1")
	before := snapshot(t, e, "k1")
	nowBefore := e.Now()
	pendingBefore := e.PendingLen()

	rejected := []error{
		e.Submit("k1", 0, "r2", ConclusionPass),    // 非本人
		e.Assign("k1", 0, "r2"),                    // 已分配
		e.Assign("k1", 9, "r2"),                    // 参数非法
		e.Submit("ghost", 0, "r1", ConclusionPass), // 案件不存在
		e.Withdraw("ghost"),                        // 案件不存在
		e.AcceptCase("k1", 0, nil, "b1", 1),        // 参数非法（重复案件号）
		e.Advance(-1),                              // 时钟回退
		e.Submit("k1", 0, "r1", ConclusionNone),    // 参数非法（结论非法）
	}
	for i, err := range rejected {
		if err == nil {
			t.Fatalf("op %d should be rejected", i)
		}
	}
	after := snapshot(t, e, "k1")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("snapshot changed:\nbefore=%+v\nafter=%+v", before, after)
	}
	if e.Now() != nowBefore || e.PendingLen() != pendingBefore {
		t.Fatalf("clock/pending changed: now %d->%d pending %d->%d",
			nowBefore, e.Now(), pendingBefore, e.PendingLen())
	}
}

// 申请分配取受理时刻最早的未分配案件；受理时刻相同按受理先后。
func TestRequestAssignFIFO(t *testing.T) {
	e := newTestEngine(t)
	mustAccept(t, e, "f2", 20, []string{"FA"}, "b1")
	mustAccept(t, e, "f1", 20, []string{"FA"}, "b1") // 同时刻，先受理 f2
	mustAccept(t, e, "f3", 30, []string{"FA"}, "b1")
	for _, want := range []string{"f2", "f1", "f3"} {
		id, slotIdx, err := e.RequestAssign("r1")
		if err != nil {
			t.Fatalf("RequestAssign: %v", err)
		}
		if id != want || slotIdx != 0 {
			t.Fatalf("got (%s,%d), want (%s,0)", id, slotIdx, want)
		}
	}
	_, _, err := e.RequestAssign("r1")
	wantErr(t, err, ErrNoPending)
}

// 双人案件第一名分配后仍留在待分配队列，第二名从同一案件取出。
func TestRequestAssignDoubleCase(t *testing.T) {
	e := newTestEngine(t)
	mustAccept(t, e, "g1", 0, []string{"FC"}, "b1")
	id, slotIdx, err := e.RequestAssign("r1")
	if err != nil || id != "g1" || slotIdx != 0 {
		t.Fatalf("first: %s %d %v", id, slotIdx, err)
	}
	// 一级复核员申请第二名空位报级别不符。
	if err := e.RegisterReviewer("r1c", "b0", LevelOne); err != nil {
		t.Fatalf("RegisterReviewer: %v", err)
	}
	_, _, err = e.RequestAssign("r1c")
	wantErr(t, err, ErrLevelMismatch)
	// 第一名本人申请第二名空位报同人复核。
	_, _, err = e.RequestAssign("r1")
	wantErr(t, err, ErrSameReviewer)
	id, slotIdx, err = e.RequestAssign("r2")
	if err != nil || id != "g1" || slotIdx != 1 {
		t.Fatalf("second: %s %d %v", id, slotIdx, err)
	}
}

// 并发：两名复核员对同一双人案件并发提交结论，最终结论与某种串行顺序一致，
// 不得出现仲裁与直接结论并存；并发申请分配同一案件只有一个成功。
func TestConcurrentSubmits(t *testing.T) {
	for iter := 0; iter < 200; iter++ {
		e := newTestEngine(t)
		mustAccept(t, e, "cc", 0, []string{"FC"}, "b1")
		mustAssign(t, e, "cc", 0, "r1")
		mustAssign(t, e, "cc", 1, "r2")
		conclusions := []Conclusion{ConclusionPass, ConclusionReject}
		if iter%2 == 0 {
			conclusions[1] = ConclusionPass
		}
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, rv := range []string{"r1", "r2"} {
			wg.Add(1)
			go func(i int, rv string) {
				defer wg.Done()
				errs[i] = e.Submit("cc", i, rv, conclusions[i])
			}(i, rv)
		}
		wg.Wait()
		if errs[0] != nil || errs[1] != nil {
			t.Fatalf("iter %d: %v %v", iter, errs[0], errs[1])
		}
		s := snapshot(t, e, "cc")
		agree := conclusions[0] == conclusions[1]
		if agree {
			want := OutcomePass
			if conclusions[0] == ConclusionReject {
				want = OutcomeReject
			}
			if s.Outcome != want || len(s.Slots) != 2 {
				t.Fatalf("iter %d agree: %+v", iter, s)
			}
		} else if s.Outcome != OutcomeNone || len(s.Slots) != 3 {
			t.Fatalf("iter %d disagree: %+v", iter, s)
		}
	}
}

// 并发申请分配：同一单人案件只有一个复核员申请成功。
func TestConcurrentRequestAssign(t *testing.T) {
	e := newTestEngine(t)
	mustAccept(t, e, "one", 0, []string{"FA"}, "b1")
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for _, rv := range []string{"r1", "r2", "r3", "r4"} {
		wg.Add(1)
		go func(rv string) {
			defer wg.Done()
			_, _, err := e.RequestAssign(rv)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				success++
			} else if err != ErrNoPending && err != ErrConflict {
				t.Errorf("unexpected err: %v", err)
			}
		}(rv)
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("success = %d, want 1", success)
	}
}
