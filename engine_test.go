package ontology

import "testing"

func mustCode(t *testing.T, err error, want Code, wantSub string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error code=%d sub=%s, got nil", want, wantSub)
	}
	ee, ok := err.(*Error)
	if !ok {
		t.Fatalf("not *Error: %v", err)
	}
	if ee.Code != want {
		t.Fatalf("code=%d want %d (%s)", ee.Code, want, err)
	}
	if wantSub != SubNone && ee.SubCode != wantSub {
		t.Fatalf("sub=%s want %s (%s)", ee.SubCode, wantSub, err)
	}
}

func baseCfg() Config {
	return Config{
		Budget: 100, Deadline: 1000, PauseBudget: 30, MaxSinglePause: 20,
		WindowLen: 10, WarnThreshold: 2, LockThreshold: 3, MaxWarnings: 3,
	}
}

// 作答时间耗尽与绝对截止同刻。
func TestBudgetAndDeadlineSameInstant(t *testing.T) {
	e := NewEngine()
	cfg := Config{Budget: 100, Deadline: 100, PauseBudget: 30, MaxSinglePause: 20,
		WindowLen: 10, WarnThreshold: 2, LockThreshold: 3, MaxWarnings: 3}
	if err := e.CreateSession("s", 0, cfg); err != nil {
		t.Fatal(err)
	}
	snap, err := e.Snapshot("s", 150) // 100 时同刻到达，150 才触及
	if err != nil {
		t.Fatal(err)
	}
	if snap.State != StateEnded || snap.EndAt != 100 || snap.UsedActive != 100 {
		t.Fatalf("snap=%+v", snap)
	}
}

// 暂停预算恰好用尽：暂停 [10,40) 恰 30。
func TestPauseBudgetExactlyUsed(t *testing.T) {
	e := NewEngine()
	cfg := baseCfg()
	cfg.MaxSinglePause = 100 // 本用例只验证暂停总预算
	if err := e.CreateSession("s", 0, cfg); err != nil {
		t.Fatal(err)
	}
	cred, err := e.Disconnect("s", 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Resume("s", 40, cred); err != nil {
		t.Fatal(err)
	}
	snap, err := e.Snapshot("s", 50)
	if err != nil {
		t.Fatal(err)
	}
	// active [0,10)+[40,50)=20；暂停 30 恰好不额外计作答。
	if snap.UsedActive != 20 || snap.TotalPaused != 30 {
		t.Fatalf("snap=%+v", snap)
	}
}

// 单次暂停恰等于上限允许；超出 1 即自动结束，落地于上限时刻。
func TestSinglePauseExactlyAtLimit(t *testing.T) {
	e := NewEngine()
	if err := e.CreateSession("s", 0, baseCfg()); err != nil {
		t.Fatal(err)
	}
	cred, _ := e.Disconnect("s", 0)
	if _, err := e.Resume("s", 20, cred); err != nil {
		t.Fatalf("equal limit should resume: %v", err)
	}

	e2 := NewEngine()
	cfg2 := baseCfg()
	cfg2.PauseBudget, cfg2.MaxSinglePause = 100, 20 // 超出 1 即触发单次上限
	if err := e2.CreateSession("s", 0, cfg2); err != nil {
		t.Fatal(err)
	}
	cred2, _ := e2.Disconnect("s", 0)
	_, err := e2.Resume("s", 21, cred2)
	mustCode(t, err, ErrSessionEnded, SubSettled)
	snap, _ := e2.Snapshot("s", 21)
	if snap.EndAt != 20 || snap.EndReason != "single_pause_limit" {
		t.Fatalf("snap=%+v", snap)
	}
}

// 旧凭证续考：第一次暂停的凭证在第二次暂停后变为旧凭证。
func TestStaleCredential(t *testing.T) {
	e := NewEngine()
	if err := e.CreateSession("s", 0, baseCfg()); err != nil {
		t.Fatal(err)
	}
	first, _ := e.Disconnect("s", 1)
	if _, err := e.Resume("s", 2, first); err != nil {
		t.Fatal(err)
	}
	second, _ := e.Disconnect("s", 3)
	if second == first {
		t.Fatal("credential must rotate")
	}
	_, err := e.Resume("s", 4, first)
	mustCode(t, err, ErrCredential, SubStaleCredential)
	// 完全无法识别的凭证（任何暂停都没签发过）。
	_, err = e.Resume("s", 5, "cred-999999")
	mustCode(t, err, ErrCredential, SubStaleCredential)
}

// 暂停前发出暂停后到达的在途作答接受；暂停后产生的拒绝；旧代次拒绝。
func TestInFlightAnswerAndGeneration(t *testing.T) {
	e := NewEngine()
	if err := e.CreateSession("s", 0, baseCfg()); err != nil {
		t.Fatal(err)
	}
	if err := e.SubmitAnswer("s", 1, 1, Answer{"q1", 5, "a"}); err != nil {
		t.Fatal(err)
	}
	cred, _ := e.Disconnect("s", 10) // 水位 = maxSeq+1 = 6
	if err := e.SubmitAnswer("s", 11, 1, Answer{"q2", 3, "inflight"}); err != nil {
		t.Fatalf("inflight answer: %v", err)
	}
	mustCode(t, e.SubmitAnswer("s", 12, 1, Answer{"q3", 7, "after-pause"}),
		ErrStateNotAllowed, SubNone)
	gen, err := e.Resume("s", 15, cred)
	if err != nil {
		t.Fatal(err)
	}
	if gen != 2 {
		t.Fatalf("gen=%d", gen)
	}
	mustCode(t, e.SubmitAnswer("s", 16, 1, Answer{"q4", 8, "oldgen"}),
		ErrCredential, SubOldGeneration)
	if err := e.SubmitAnswer("s", 17, 2, Answer{"q4", 8, "newgen"}); err != nil {
		t.Fatal(err)
	}
}

// 同一题目乱序：以最大序号为准，迟到与重复分别可区分。
func TestOutOfOrderSameQuestion(t *testing.T) {
	e := NewEngine()
	if err := e.CreateSession("s", 0, baseCfg()); err != nil {
		t.Fatal(err)
	}
	rows := []Answer{
		{"q", 10, "v10"},
		{"q", 20, "v20"},
		{"q", 15, "v15"},
		{"q", 10, "dup"},
		{"q", 25, "v25"},
	}
	if err := e.SubmitAnswer("s", 1, 1, rows[0]); err != nil {
		t.Fatal(err)
	}
	if err := e.SubmitAnswer("s", 2, 1, rows[1]); err != nil {
		t.Fatal(err)
	}
	mustCode(t, e.SubmitAnswer("s", 3, 1, rows[2]), ErrAnswerLateOrDuplicate, SubLateAnswer)
	mustCode(t, e.SubmitAnswer("s", 4, 1, rows[3]), ErrAnswerLateOrDuplicate, SubDuplicateAnswer)
	if err := e.SubmitAnswer("s", 5, 1, rows[4]); err != nil {
		t.Fatal(err)
	}
	snap, _ := e.Snapshot("s", 6)
	if got := snap.Answers["q"]; got.Seq != 25 || got.Text != "v25" {
		t.Fatalf("final=%+v", got)
	}
}

// 窗口左开右闭：W=10，t=1 事件在 t=10 仍在窗、t=11 离窗。
func TestWindowOpenClosedBoundary(t *testing.T) {
	e := NewEngine()
	cfg := baseCfg()
	cfg.WarnThreshold, cfg.LockThreshold = 5, 6
	if err := e.CreateSession("s", 0, cfg); err != nil {
		t.Fatal(err)
	}
	if err := e.RecordEvent("s", 1, "blur"); err != nil {
		t.Fatal(err)
	}
	if s, _ := e.Snapshot("s", 10); s.WindowCount != 1 {
		t.Fatalf("count@10=%d", s.WindowCount)
	}
	if s, _ := e.Snapshot("s", 11); s.WindowCount != 0 {
		t.Fatalf("count@11=%d", s.WindowCount)
	}
}

// 警告阈值与锁定阈值同时达到；锁定中作答拒绝但作答时间照常累计。
func TestWarnAndLockTogether(t *testing.T) {
	e := NewEngine()
	if err := e.CreateSession("s", 0, baseCfg()); err != nil {
		t.Fatal(err)
	}
	for _, at := range []int64{1, 2, 3} {
		if err := e.RecordEvent("s", at, "blur"); err != nil {
			t.Fatal(err)
		}
	}
	snap, _ := e.Snapshot("s", 3)
	if snap.Warnings != 1 || !snap.Locked || snap.State != StateLocked {
		t.Fatalf("snap=%+v", snap)
	}
	mustCode(t, e.SubmitAnswer("s", 4, 1, Answer{"q", 1, "x"}),
		ErrStateNotAllowed, SubNone)
	snap2, _ := e.Snapshot("s", 10)
	if snap2.UsedActive != 10 {
		t.Fatalf("used=%d", snap2.UsedActive)
	}
}

// 解除锁定后窗口残留事件再次触发警告与锁定。
func TestRelockFromResidualWindow(t *testing.T) {
	e := NewEngine()
	if err := e.CreateSession("s", 0, baseCfg()); err != nil {
		t.Fatal(err)
	}
	for _, at := range []int64{1, 2, 3} {
		if err := e.RecordEvent("s", at, "blur"); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Unlock("s", 4); err != nil {
		t.Fatal(err)
	}
	snap, _ := e.Snapshot("s", 4)
	if snap.WindowCount != 3 || snap.Warnings != 1 {
		t.Fatalf("residual count=%d warn=%d", snap.WindowCount, snap.Warnings)
	}
	if err := e.RecordEvent("s", 5, "blur"); err != nil {
		t.Fatal(err)
	}
	snap2, _ := e.Snapshot("s", 5)
	if snap2.Warnings != 2 || snap2.State != StateLocked {
		t.Fatalf("snap=%+v", snap2)
	}
}

// 警告累计达到上限：违规结束并结算。
func TestWarningLimitViolation(t *testing.T) {
	e := NewEngine()
	cfg := baseCfg()
	cfg.WarnThreshold, cfg.LockThreshold = 1, 10
	if err := e.CreateSession("s", 0, cfg); err != nil {
		t.Fatal(err)
	}
	// 窗口 W=10：事件间隔 11 使每次窗口回落，3 次警告后违规。
	for _, at := range []int64{1, 12, 23} {
		if err := e.RecordEvent("s", at, "blur"); err != nil {
			t.Fatal(err)
		}
	}
	snap, _ := e.Snapshot("s", 24)
	if !snap.Violation || !snap.Settled || snap.Warnings != 3 || snap.EndAt != 23 {
		t.Fatalf("snap=%+v", snap)
	}
}

// 延长预算只推迟预算耗尽，不改变绝对截止。
func TestExtendKeepsDeadline(t *testing.T) {
	e := NewEngine()
	cfg := Config{Budget: 10, Deadline: 50, PauseBudget: 30, MaxSinglePause: 20,
		WindowLen: 10, WarnThreshold: 2, LockThreshold: 3, MaxWarnings: 3}
	if err := e.CreateSession("s", 0, cfg); err != nil {
		t.Fatal(err)
	}
	if err := e.Extend("s", 5, 100); err != nil {
		t.Fatal(err)
	}
	snap, _ := e.Snapshot("s", 60)
	if snap.State != StateEnded || snap.EndAt != 50 || snap.EndReason != "deadline" {
		t.Fatalf("snap=%+v", snap)
	}
	// 已结束会话延长被拒绝。
	mustCode(t, e.Extend("s", 61, 10), ErrSessionEnded, SubSettled)
}

// 主动提交后结算不可变，重复提交可区分。
func TestSubmitSettledImmutable(t *testing.T) {
	e := NewEngine()
	if err := e.CreateSession("s", 0, baseCfg()); err != nil {
		t.Fatal(err)
	}
	if err := e.SubmitAnswer("s", 1, 1, Answer{"q", 1, "v"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Submit("s", 5); err != nil {
		t.Fatal(err)
	}
	mustCode(t, e.Submit("s", 6), ErrSessionEnded, SubSettled)
	snap, _ := e.Snapshot("s", 6)
	if !snap.Settled || snap.UsedActive != 5 || snap.Answers["q"].Text != "v" {
		t.Fatalf("snap=%+v", snap)
	}
}

// 被拒绝的操作不得推进服务端时钟。
func TestRejectedOpDoesNotAdvanceClock(t *testing.T) {
	e := NewEngine()
	if err := e.CreateSession("s", 0, baseCfg()); err != nil {
		t.Fatal(err)
	}
	_ = e.SubmitAnswer("s", 100, 0, Answer{"q", 1, "x"}) // 参数非法：时钟不推进
	if err := e.CreateSession("s2", 50, baseCfg()); err != nil {
		t.Fatalf("clock should still allow 50: %v", err)
	}
	mustCode(t, e.CreateSession("s3", 40, baseCfg()), ErrClockRegression, SubNone)
}

// 错误优先级逐对验证：每对相邻优先级构造同时满足的输入，取高者。
func TestErrorPriorityPairs(t *testing.T) {
	var err error
	// 1>2 参数非法 且 时钟回退：非法优先。
	e := NewEngine()
	if err := e.CreateSession("s", 10, baseCfg()); err != nil {
		t.Fatal(err)
	}
	mustCode(t, e.SubmitAnswer("s", 5, 1, Answer{"", 1, "x"}), ErrInvalidParam, SubNone)

	// 2>3 时钟回退 且 会话不存在：回退优先。
	_, err = e.Snapshot("missing", 5)
	mustCode(t, err, ErrClockRegression, SubNone)

	// 3>4 不存在 且 已结束（无从谈起，用不存在+任意）：不存在优先。
	mustCode(t, e.Submit("nope", 20), ErrSessionNotFound, SubNone)

	// 4>5 已结束 且 凭证问题：已结束会话上重复提交按已结束拒绝。
	e2 := NewEngine()
	if err := e2.CreateSession("s", 0, baseCfg()); err != nil {
		t.Fatal(err)
	}
	cred, _ := e2.Disconnect("s", 1)
	if _, err := e2.Resume("s", 2, cred); err != nil {
		t.Fatal(err)
	}
	if err := e2.Submit("s", 5); err != nil {
		t.Fatal(err)
	}
	mustCode(t, e2.Submit("s", 6), ErrSessionEnded, SubSettled)

	// 5>6 旧代次 且 锁定中作答：旧代次优先。
	e3 := NewEngine()
	if err := e3.CreateSession("s", 0, baseCfg()); err != nil {
		t.Fatal(err)
	}
	for _, at := range []int64{1, 2, 3} {
		if err := e3.RecordEvent("s", at, "blur"); err != nil {
			t.Fatal(err)
		}
	}
	mustCode(t, e3.SubmitAnswer("s", 4, 1, Answer{"q", 1, "x"}),
		ErrStateNotAllowed, SubNone) // 同一代次时锁定拒绝

	// 5>6 非暂停状态续考且凭证错误：此前无任何暂停签发 -> 未知凭证。
	_, err = e3.Resume("s", 5, "cred-12345")
	mustCode(t, err, ErrCredential, SubUnknownCredential)

	// 6>7 暂停中作答 且 序号迟到/重复：状态优先（非在途作答）。
	e4 := NewEngine()
	if err := e4.CreateSession("s", 0, baseCfg()); err != nil {
		t.Fatal(err)
	}
	if err := e4.SubmitAnswer("s", 1, 1, Answer{"q", 5, "x"}); err != nil {
		t.Fatal(err)
	}
	_, _ = e4.Disconnect("s", 2)
	mustCode(t, e4.SubmitAnswer("s", 3, 1, Answer{"q2", 99, "y"}),
		ErrStateNotAllowed, SubNone)
	// 同一请求若为重复序号：重复判定仍在状态之后（在途且重复 -> 重复）。
	mustCode(t, e4.SubmitAnswer("s", 4, 1, Answer{"q", 5, "z"}),
		ErrAnswerLateOrDuplicate, SubDuplicateAnswer)
}
