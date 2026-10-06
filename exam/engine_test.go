package exam

import (
	"errors"
	"testing"
)

func testConfig() Config {
	return Config{
		AnswerBudget:   100,
		PauseBudget:    10,
		SinglePauseMax: 20,
		Window:         10,
		WarnThreshold:  2,
		LockThreshold:  3,
		WarnLimit:      5,
	}
}

func newEngine(t *testing.T, cfg Config) *Engine {
	t.Helper()
	e, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func mustStart(t *testing.T, e *Engine, id string, now, deadline int64) int64 {
	t.Helper()
	gen, err := e.StartSession(id, now, deadline)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	return gen
}

func mustErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("want error %v, got %v", want, err)
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// 作答时间耗尽与绝对截止同刻：恰等于视为到达，落地时刻取该共同时刻。
func TestAutoEndBudgetEqualsDeadline(t *testing.T) {
	cfg := testConfig()
	cfg.AnswerBudget = 50
	e := newEngine(t, cfg)
	mustStart(t, e, "s", 0, 50)

	if _, ok, err := e.SettlementOf("s", 49); err != nil || ok {
		t.Fatalf("at 49 should be ongoing, ok=%v err=%v", ok, err)
	}
	set, ok, err := e.SettlementOf("s", 50)
	mustOK(t, err)
	if !ok {
		t.Fatal("at 50 should be settled")
	}
	if set.EndAt != 50 || set.EffectiveAnswerTime != 50 {
		t.Fatalf("EndAt=%d used=%d, want 50/50", set.EndAt, set.EffectiveAnswerTime)
	}
	if set.Reason != EndReasonTimeExhausted {
		t.Fatalf("reason=%v, want time-exhausted (budget wins ties)", set.Reason)
	}
	// 惰性落地：触及时刻晚于应结束时刻，落地仍取应结束时刻。
	e2 := newEngine(t, cfg)
	mustStart(t, e2, "s", 0, 50)
	set2, ok, err := e2.SettlementOf("s", 80)
	mustOK(t, err)
	if !ok || set2.EndAt != 50 {
		t.Fatalf("lazy landing: ok=%v EndAt=%d, want true/50", ok, set2.EndAt)
	}
}

// 暂停预算恰好用尽：暂停不计作答时间，也无超出部分。
func TestPauseBudgetExactlyUsed(t *testing.T) {
	e := newEngine(t, testConfig())
	gen := mustStart(t, e, "s", 0, 1000)

	tok, err := e.Disconnect("s", 5)
	mustOK(t, err)
	gen2, err := e.Resume("s", tok, 15) // 暂停 10，恰等于预算
	mustOK(t, err)
	if gen2 != gen+1 {
		t.Fatalf("generation=%d, want %d", gen2, gen+1)
	}
	set, err := e.Submit("s", 40)
	mustOK(t, err)
	if set.TotalPauseTime != 10 {
		t.Fatalf("TotalPauseTime=%d, want 10", set.TotalPauseTime)
	}
	if want := int64(30); set.EffectiveAnswerTime != want { // 5 + (40-15)
		t.Fatalf("EffectiveAnswerTime=%d, want %d", set.EffectiveAnswerTime, want)
	}
}

// 暂停超出预算的部分照常累计为作答时间。
func TestPauseExcessCountsAsAnswerTime(t *testing.T) {
	e := newEngine(t, testConfig())
	mustStart(t, e, "s", 0, 1000)

	tok, err := e.Disconnect("s", 5)
	mustOK(t, err)
	if _, err := e.Resume("s", tok, 25); err != nil { // 暂停 20，预算 10，超出 10
		mustOK(t, err)
	}
	set, err := e.Submit("s", 40)
	mustOK(t, err)
	if set.TotalPauseTime != 20 {
		t.Fatalf("TotalPauseTime=%d, want 20", set.TotalPauseTime)
	}
	if want := int64(30); set.EffectiveAnswerTime != want { // 5 + 10(excess) + (40-25)
		t.Fatalf("EffectiveAnswerTime=%d, want %d", set.EffectiveAnswerTime, want)
	}
}

// 单次暂停恰等于上限不结束，超过 1 个时刻才结束。
func TestSinglePauseExactlyLimit(t *testing.T) {
	e := newEngine(t, testConfig()) // SinglePauseMax = 20
	mustStart(t, e, "s", 0, 1000)

	_, err := e.Disconnect("s", 5)
	mustOK(t, err)
	if _, ok, err := e.SettlementOf("s", 25); err != nil || ok { // 5+20，恰等于上限
		t.Fatalf("at limit should be ongoing, ok=%v err=%v", ok, err)
	}
	set, ok, err := e.SettlementOf("s", 26) // 超过上限 1 个时刻
	mustOK(t, err)
	if !ok {
		t.Fatal("beyond limit should be settled")
	}
	if set.Reason != EndReasonPauseLimit || set.EndAt != 26 {
		t.Fatalf("reason=%v EndAt=%d, want pause-limit/26", set.Reason, set.EndAt)
	}
}

// 旧凭证续考被拒绝且可区分；只有最近一次暂停的凭证有效。
func TestOldTokenRejected(t *testing.T) {
	e := newEngine(t, testConfig())
	mustStart(t, e, "s", 0, 1000)

	tok1, err := e.Disconnect("s", 5)
	mustOK(t, err)
	_, err = e.Resume("s", tok1, 8)
	mustOK(t, err)
	tok2, err := e.Disconnect("s", 10)
	mustOK(t, err)
	if _, err := e.Resume("s", tok1, 12); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("old token: want ErrInvalidToken, got %v", err)
	}
	if _, err := e.Resume("s", tok2, 12); err != nil {
		t.Fatalf("latest token should work: %v", err)
	}
	// 已消费的凭证再次使用同样被拒绝。
	if _, err := e.Disconnect("s", 15); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if _, err := e.Resume("s", tok2, 16); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("consumed token: want ErrInvalidToken, got %v", err)
	}
}

// 暂停前发出、暂停后到达的作答（序号小于暂停水位）仍被接受。
func TestAnswerIssuedBeforePauseAccepted(t *testing.T) {
	e := newEngine(t, testConfig())
	gen := mustStart(t, e, "s", 0, 1000)

	mustOK(t, e.SubmitAnswer("s", 3, Answer{QuestionID: "q1", Seq: 5, Generation: gen, Payload: "a5"}))
	tok, err := e.Disconnect("s", 4) // 水位 = 5
	mustOK(t, err)
	// seq 3 < 5：暂停前发出，接受。
	mustOK(t, e.SubmitAnswer("s", 6, Answer{QuestionID: "q2", Seq: 3, Generation: gen, Payload: "b3"}))
	// seq 5 == 水位：按暂停中作答拒绝。
	mustErr(t, e.SubmitAnswer("s", 6, Answer{QuestionID: "q1", Seq: 5, Generation: gen, Payload: "x"}), ErrAnswerWhilePaused)
	// seq 9 > 水位：暂停后发出，拒绝。
	mustErr(t, e.SubmitAnswer("s", 6, Answer{QuestionID: "q1", Seq: 9, Generation: gen, Payload: "x"}), ErrAnswerWhilePaused)

	if _, err := e.Resume("s", tok, 8); err != nil {
		t.Fatalf("resume: %v", err)
	}
	set, err := e.Submit("s", 9)
	mustOK(t, err)
	if set.Answers["q2"].Payload != "b3" || set.Answers["q1"].Payload != "a5" {
		t.Fatalf("answers=%v", set.Answers)
	}
}

// 同一题目序号乱序到达：最大序号者为准，迟到与重复可区分。
func TestOutOfOrderSameQuestion(t *testing.T) {
	e := newEngine(t, testConfig())
	gen := mustStart(t, e, "s", 0, 1000)

	mustOK(t, e.SubmitAnswer("s", 1, Answer{QuestionID: "q", Seq: 10, Generation: gen, Payload: "v10"}))
	mustErr(t, e.SubmitAnswer("s", 2, Answer{QuestionID: "q", Seq: 5, Generation: gen, Payload: "v5"}), ErrLateAnswer)
	mustErr(t, e.SubmitAnswer("s", 3, Answer{QuestionID: "q", Seq: 10, Generation: gen, Payload: "v10b"}), ErrDuplicateAnswer)
	mustOK(t, e.SubmitAnswer("s", 4, Answer{QuestionID: "q", Seq: 12, Generation: gen, Payload: "v12"}))
	// 序号不要求连续。
	mustOK(t, e.SubmitAnswer("s", 5, Answer{QuestionID: "q", Seq: 100, Generation: gen, Payload: "v100"}))

	set, err := e.Submit("s", 6)
	mustOK(t, err)
	if got := set.Answers["q"]; got.Seq != 100 || got.Payload != "v100" {
		t.Fatalf("final answer=%+v, want seq 100 v100", got)
	}
}

// 滑动窗口左开右闭：(now-W, now]，恰在左边界的被排除，右边界计入。
func TestWindowLeftOpenRightClosed(t *testing.T) {
	cfg := testConfig() // Window=10, Warn=2, Lock=3
	e := newEngine(t, cfg)
	mustStart(t, e, "s", 0, 1000)

	mustOK(t, e.RecordEvent("s", 1, EventLeavePage, 0))
	mustOK(t, e.RecordEvent("s", 5, EventLeavePage, 0)) // 窗口 {1,5} → 警告 1
	mustOK(t, e.RecordEvent("s", 11, EventLeavePage, 0))
	// t=11 时窗口为 (1,11] = {5,11}：t=1 的事件被排除，否则将触发锁定。
	s := e.sessions["s"]
	if s.status == StatusLocked {
		t.Fatal("t=1 event must be evicted at t=11 (left-open)")
	}
	if s.warnings != 2 {
		t.Fatalf("warnings=%d, want 2", s.warnings)
	}
	// 右边界闭合：t=15 时窗口 (5,15] = {11,15}，再加一条 t=15 即达 3 条锁定。
	mustOK(t, e.RecordEvent("s", 15, EventSwitchWindow, 0))
	if s.status == StatusLocked {
		t.Fatal("count=2 at t=15, should not lock yet")
	}
	mustOK(t, e.RecordEvent("s", 15, EventSwitchWindow, 0)) // 同一时刻按上报顺序逐条计入
	if s.status != StatusLocked {
		t.Fatal("count=3 at t=15, should lock")
	}
}

// 警告阈值与锁定阈值同时达到：一次事件同时记警告并锁定。
func TestWarnAndLockSimultaneous(t *testing.T) {
	cfg := testConfig()
	cfg.WarnThreshold = 2
	cfg.LockThreshold = 2
	e := newEngine(t, cfg)
	mustStart(t, e, "s", 0, 1000)

	mustOK(t, e.RecordEvent("s", 1, EventLeavePage, 0))
	mustOK(t, e.RecordEvent("s", 2, EventLeavePage, 0))
	s := e.sessions["s"]
	if s.warnings != 1 || s.status != StatusLocked {
		t.Fatalf("warnings=%d status=%v, want 1/locked", s.warnings, s.status)
	}
}

// 解除锁定后窗口残留事件不清空，新事件再次触发锁定。
func TestUnlockResidualEventsRetrigger(t *testing.T) {
	e := newEngine(t, testConfig()) // Window=10, Lock=3
	mustStart(t, e, "s", 0, 1000)

	mustOK(t, e.RecordEvent("s", 1, EventLeavePage, 0))
	mustOK(t, e.RecordEvent("s", 2, EventLeavePage, 0))
	mustOK(t, e.RecordEvent("s", 3, EventLeavePage, 0)) // 锁定
	mustOK(t, e.Unlock("s", 5))
	if e.sessions["s"].status != StatusActive {
		t.Fatal("should be active after unlock")
	}
	// 窗口残留 {1,2,3}，新事件 t=6 使计数回到 3，再次锁定。
	mustOK(t, e.RecordEvent("s", 6, EventLeavePage, 0))
	if e.sessions["s"].status != StatusLocked {
		t.Fatal("residual window events should retrigger lock")
	}
}

// 锁定期间作答被拒绝，作答时间照常累计。
func TestLockedAnswerRejectedTimeAccrues(t *testing.T) {
	e := newEngine(t, testConfig())
	gen := mustStart(t, e, "s", 0, 1000)

	mustOK(t, e.RecordEvent("s", 1, EventLeavePage, 0))
	mustOK(t, e.RecordEvent("s", 2, EventLeavePage, 0))
	mustOK(t, e.RecordEvent("s", 3, EventLeavePage, 0)) // 锁定于 t=3
	mustErr(t, e.SubmitAnswer("s", 4, Answer{QuestionID: "q", Seq: 1, Generation: gen, Payload: "x"}), ErrAnswerWhileLocked)
	mustOK(t, e.Unlock("s", 8))
	set, err := e.Submit("s", 10)
	mustOK(t, err)
	if set.EffectiveAnswerTime != 10 { // 锁定期间照常累计
		t.Fatalf("EffectiveAnswerTime=%d, want 10", set.EffectiveAnswerTime)
	}
}

// 警告次数达到上限：会话直接结束且结算为违规。
func TestWarnLimitViolation(t *testing.T) {
	cfg := testConfig()
	cfg.WarnThreshold = 1
	cfg.LockThreshold = 100 // 不触发锁定
	cfg.WarnLimit = 3
	e := newEngine(t, cfg)
	mustStart(t, e, "s", 0, 1000)

	mustOK(t, e.RecordEvent("s", 1, EventLeavePage, 0))
	mustOK(t, e.RecordEvent("s", 2, EventLeavePage, 0))
	mustOK(t, e.RecordEvent("s", 3, EventLeavePage, 0)) // 第 3 次警告 → 违规结束
	set, ok, err := e.SettlementOf("s", 3)
	mustOK(t, err)
	if !ok || !set.Violation || set.Reason != EndReasonViolation || set.Warnings != 3 {
		t.Fatalf("settlement=%+v ok=%v", set, ok)
	}
	if set.EndAt != 3 {
		t.Fatalf("EndAt=%d, want 3", set.EndAt)
	}
}

// 延长预算只影响作答预算，不改变绝对截止时刻；已结束会话拒绝延长。
func TestExtendKeepsDeadline(t *testing.T) {
	cfg := testConfig()
	cfg.AnswerBudget = 5
	e := newEngine(t, cfg)
	mustStart(t, e, "s", 0, 8)

	mustOK(t, e.Extend("s", 2, 100)) // 预算 105，截止仍为 8
	set, ok, err := e.SettlementOf("s", 9)
	mustOK(t, err)
	if !ok || set.EndAt != 8 || set.Reason != EndReasonDeadline {
		t.Fatalf("deadline must stay 8: %+v ok=%v", set, ok)
	}
	mustErr(t, e.Extend("s", 9, 10), ErrSessionEnded)

	// 延长使原本会耗尽的会话得以继续。
	e2 := newEngine(t, cfg)
	mustStart(t, e2, "s", 0, 1000)
	mustOK(t, e2.Extend("s", 4, 10)) // 预算 5→15
	if _, ok, _ := e2.SettlementOf("s", 6); ok {
		t.Fatal("extended session should still be ongoing at 6")
	}
	set2, ok, _ := e2.SettlementOf("s", 20)
	if !ok || set2.EndAt != 15 || set2.Reason != EndReasonTimeExhausted {
		t.Fatalf("settlement=%+v ok=%v, want EndAt=15 time-exhausted", set2, ok)
	}
}

// 续考后旧代次的作答一律拒绝。
func TestStaleGenerationRejected(t *testing.T) {
	e := newEngine(t, testConfig())
	gen := mustStart(t, e, "s", 0, 1000)

	tok, err := e.Disconnect("s", 2)
	mustOK(t, err)
	gen2, err := e.Resume("s", tok, 4)
	mustOK(t, err)
	mustErr(t, e.SubmitAnswer("s", 5, Answer{QuestionID: "q", Seq: 1, Generation: gen, Payload: "old"}), ErrStaleGeneration)
	mustOK(t, e.SubmitAnswer("s", 5, Answer{QuestionID: "q", Seq: 1, Generation: gen2, Payload: "new"}))
}

// 结算不可变，重复提交被拒绝且可区分。
func TestDuplicateSubmitRejected(t *testing.T) {
	e := newEngine(t, testConfig())
	mustStart(t, e, "s", 0, 1000)

	set1, err := e.Submit("s", 10)
	mustOK(t, err)
	if _, err := e.Submit("s", 11); !errors.Is(err, ErrAlreadySettled) {
		t.Fatalf("want ErrAlreadySettled, got %v", err)
	}
	set2, ok, err := e.SettlementOf("s", 12)
	mustOK(t, err)
	if !ok || set2.EndAt != set1.EndAt || set2.EffectiveAnswerTime != set1.EffectiveAnswerTime {
		t.Fatalf("settlement mutated: %+v vs %+v", set1, set2)
	}
}

// 错误优先级逐对验证：同时违反两条规则时返回高优先级错误。
func TestErrorPriorityPairs(t *testing.T) {
	cfg := testConfig()

	// 参数非法 > 时钟回退
	e := newEngine(t, cfg)
	mustStart(t, e, "s", 10, 1000)
	mustErr(t, e.SubmitAnswer("s", 5, Answer{QuestionID: "", Seq: 1, Generation: 1}), ErrInvalidParam)

	// 时钟回退 > 会话不存在
	mustErr(t, e.SubmitAnswer("ghost", 5, Answer{QuestionID: "q", Seq: 1, Generation: 1}), ErrClockRegression)

	// 会话不存在 > 会话已结束（不存在优先被检出）
	if _, _, err := e.SettlementOf("ghost", 10); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("want ErrSessionNotFound, got %v", err)
	}

	// 会话已结束 > 凭证无效
	if _, err := e.Submit("s", 20); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Resume("s", "bogus", 21); !errors.Is(err, ErrSessionEnded) {
		t.Fatalf("want ErrSessionEnded, got %v", err)
	}
	// 会话已结束 > 代次过期
	mustErr(t, e.SubmitAnswer("s", 21, Answer{QuestionID: "q", Seq: 1, Generation: 99, Payload: "x"}), ErrSessionEnded)

	// 凭证无效 > 状态不允许（非暂停状态持错误凭证续考）
	e2 := newEngine(t, cfg)
	mustStart(t, e2, "s", 0, 1000)
	if _, err := e2.Resume("s", "bogus", 1); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}

	// 代次过期 > 状态不允许（锁定中持旧代次作答）
	e3 := newEngine(t, cfg)
	gen := mustStart(t, e3, "s", 0, 1000)
	mustOK(t, e3.RecordEvent("s", 1, EventLeavePage, 0))
	mustOK(t, e3.RecordEvent("s", 2, EventLeavePage, 0))
	mustOK(t, e3.RecordEvent("s", 3, EventLeavePage, 0)) // 锁定
	mustErr(t, e3.SubmitAnswer("s", 4, Answer{QuestionID: "q", Seq: 1, Generation: gen + 1, Payload: "x"}), ErrStaleGeneration)

	// 状态不允许 > 作答迟到（暂停中且序号低于水位但高于已落定序号的场景：
	// 序号高于水位时优先报暂停中作答）
	e4 := newEngine(t, cfg)
	gen4 := mustStart(t, e4, "s", 0, 1000)
	mustOK(t, e4.SubmitAnswer("s", 1, Answer{QuestionID: "q", Seq: 10, Generation: gen4, Payload: "a"}))
	if _, err := e4.Disconnect("s", 2); err != nil {
		t.Fatal(err)
	}
	mustErr(t, e4.SubmitAnswer("s", 3, Answer{QuestionID: "q", Seq: 20, Generation: gen4, Payload: "b"}), ErrAnswerWhilePaused)
}

// 判定开销不随历史增长：内部状态规模有界（每题一条、窗口有界）。
func TestInternalStateBounded(t *testing.T) {
	cfg := testConfig()
	cfg.WarnLimit = 1 << 30
	cfg.LockThreshold = 1 << 30
	cfg.AnswerBudget = 1 << 40
	e := newEngine(t, cfg)
	gen := mustStart(t, e, "s", 0, 1_000_000)

	const n = 20000
	for i := 0; i < n; i++ {
		mustOK(t, e.SubmitAnswer("s", int64(i), Answer{QuestionID: "q", Seq: int64(i), Generation: gen, Payload: "v"}))
		mustOK(t, e.RecordEvent("s", int64(i), EventLeavePage, 0))
	}
	s := e.sessions["s"]
	if len(s.landed) != 1 || len(s.maxSeq) != 1 {
		t.Fatalf("per-question state must stay O(1): landed=%d maxSeq=%d", len(s.landed), len(s.maxSeq))
	}
	if got := int64(len(s.window) - s.whead); got > cfg.Window+1 {
		t.Fatalf("window length=%d, want <= %d", got, cfg.Window+1)
	}
}
