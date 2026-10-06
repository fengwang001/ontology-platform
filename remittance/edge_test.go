package remittance

import "testing"

// 制裁命中：不消耗报价、不记幂等键、不留额度占用。
func TestSanctionDoesNotConsume(t *testing.T) {
	e := testEngine()
	addTestSender(t, e, "s1", 100, 100, 100)
	e.AddSanctionedPayee("bad")
	q := mustQuote(t, e, "s1", 1, 100, 0)

	if _, err := e.Submit(SubmitRequest{"s1", q, "bad", "k", 1}); CodeOf(err) != ErrCodeSanctionedPayee {
		t.Fatalf("want SanctionedPayee, got %v", err)
	}
	e.RemoveSanctionedPayee("bad")
	// 同键、同报价在移出名单后成功 => 键未记录、报价未消耗、无占用残留。
	res := mustSubmit(t, e, "s1", q, "bad", "k", 2)
	if res.Status != StatusSucceeded {
		t.Fatalf("want success after delisting, got %s", res.Status)
	}
}

// 幂等：同参重放返回原结果且不重复占用；异参冲突。
func TestIdempotencyReplayAndConflict(t *testing.T) {
	e := New(Config{QuoteTTLSeconds: 100000, ReviewSeconds: 60, ReviewThreshold: 100})
	addTestSender(t, e, "s1", 100000, 100000, 100000)
	q := mustQuote(t, e, "s1", 1, 1000000000, 0) // target=1000 待审核
	first := mustSubmit(t, e, "s1", q, "p", "key", 1)
	if first.Status != StatusPending {
		t.Fatalf("want pending, got %+v", first)
	}
	if err := e.Approve(first.TransferID, 10); err != nil {
		t.Fatalf("approve: %v", err)
	}
	replay, err := e.Submit(SubmitRequest{"s1", q, "p", "key", 30})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replay.Replay || replay.TransferID != first.TransferID || replay.Status != StatusPending {
		t.Fatalf("replay must return original Pending result, got %+v", replay)
	}
	// 异参（报价编号不同）在报价查找之前即报冲突，直接用不存在的编号即可。
	if _, err := e.Submit(SubmitRequest{"s1", 9999, "p", "key", 40}); CodeOf(err) != ErrCodeIdempotencyConflict {
		t.Fatalf("want IdempotencyConflict, got %v", err)
	}
	if u := mustUsage(t, e, "s1", 40); u.DayUsed != 1000 {
		t.Fatalf("replay must not double-occupy, got dayUsed=%d", u.DayUsed)
	}
}

// 审核逾期：恰等第 R 秒批准成功；晚一秒失败释放；失败后操作均非法。
func TestReviewDeadlineBoundary(t *testing.T) {
	newE := func() *Engine {
		e := New(Config{QuoteTTLSeconds: 1_000_000, ReviewSeconds: 60, ReviewThreshold: 100})
		addTestSender(t, e, "s1", 100000, 100000, 100000)
		return e
	}

	// 恰等 60 秒批准
	e := newE()
	q := mustQuote(t, e, "s1", 1, 100000000, 0)
	res := mustSubmit(t, e, "s1", q, "p", "k", 0)
	if err := e.Approve(res.TransferID, 60); err != nil {
		t.Fatalf("approve at exactly R should succeed: %v", err)
	}
	info, err := e.GetTransfer(res.TransferID, 60)
	if err != nil || info.Status != StatusSucceeded {
		t.Fatalf("want succeeded at deadline, got %+v err=%v", info, err)
	}

	// 晚 1 秒
	e = newE()
	q = mustQuote(t, e, "s1", 1, 100000000, 0)
	res = mustSubmit(t, e, "s1", q, "p", "k", 0)
	if err := e.Approve(res.TransferID, 61); CodeOf(err) != ErrCodeIllegalState {
		t.Fatalf("approve after deadline want IllegalState, got %v", err)
	}
	info, err = e.GetTransfer(res.TransferID, 61)
	if err != nil || info.Status != StatusFailed || info.DecidedAt != 61 {
		t.Fatalf("want failed decidedAt=61, got %+v err=%v", info, err)
	}
	u := mustUsage(t, e, "s1", 61)
	if u.DayUsed != 0 || u.AnnualUsed != 0 {
		t.Fatalf("expired failure must release all, got %+v", u)
	}
	if err := e.Withdraw(res.TransferID, 62); CodeOf(err) != ErrCodeIllegalState {
		t.Fatalf("withdraw after fail want IllegalState, got %v", err)
	}
}

// 撤回等同拒绝，立即释放。
func TestWithdrawReleases(t *testing.T) {
	e := New(Config{QuoteTTLSeconds: 1_000_000, ReviewSeconds: 60, ReviewThreshold: 100})
	addTestSender(t, e, "s1", 100000, 100000, 100000)
	q := mustQuote(t, e, "s1", 1, 100000000, 0)
	res := mustSubmit(t, e, "s1", q, "p", "k", 0)
	u := mustUsage(t, e, "s1", 5)
	if u.DayUsed != 100 {
		t.Fatalf("pending must occupy, got %d", u.DayUsed)
	}
	if err := e.Withdraw(res.TransferID, 10); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	info, _ := e.GetTransfer(res.TransferID, 10)
	if info.Status != StatusFailed || info.DecidedAt != 10 {
		t.Fatalf("want failed at 10, got %+v", info)
	}
	u = mustUsage(t, e, "s1", 10)
	if u.DayUsed != 0 {
		t.Fatalf("withdraw must release, got %d", u.DayUsed)
	}
}

// 跨日后逾期：释放归属原日（day 0），而不是查询当日（day 1）。
func TestReleaseAcrossDay(t *testing.T) {
	e := New(Config{QuoteTTLSeconds: 1_000_000, ReviewSeconds: 30, ReviewThreshold: 100})
	addTestSender(t, e, "s1", 100000, 100, 100000)
	q := mustQuote(t, e, "s1", 1, 100000000, 0) // day0 占用 100，恰等日限额
	res := mustSubmit(t, e, "s1", q, "p", "k", 0)

	// day 1 逾期物化：day0 应释放 100，day1 占用为 0。
	u := mustUsage(t, e, "s1", 86400)
	if u.Day != 1 || u.DayUsed != 0 {
		t.Fatalf("day1 usage must be 0, got %+v", u)
	}
	// day0 的释放不体现在 day1；查 day0 视角（用 GetTransfer 已物化过）。
	info, _ := e.GetTransfer(res.TransferID, 86400)
	if info.Status != StatusFailed || info.Day != 0 || info.DecidedAt != 31 {
		t.Fatalf("release must point to day0, got %+v", info)
	}
	// 释放确实回到原日：day0 已空，可在 day1 重新用满日限额。
	q2 := mustQuote(t, e, "s1", 1, 100000000, 86400)
	r2 := mustSubmit(t, e, "s1", q2, "p2", "k2", 86400)
	if r2.Status != StatusPending {
		t.Fatalf("day1 should accept a new transfer (pending is fine), got %s", r2.Status)
	}
}

// 跨年度（>=365 天）后才失败：释放回到原日且不影响当前年度窗口。
func TestReleaseAcrossYear(t *testing.T) {
	e := New(Config{QuoteTTLSeconds: 1_000_000, ReviewSeconds: 10, ReviewThreshold: 100})
	addTestSender(t, e, "s1", 100000, 100000, 100)
	q := mustQuote(t, e, "s1", 1, 100000000, 0) // 占用恰等年度额度 100
	res := mustSubmit(t, e, "s1", q, "p", "k", 0)

	// 365 天之后：原占用日 day0 与当前 day365 之差 == 365，已不在滚动窗口。
	day365 := int64(365) * 86400
	u := mustUsage(t, e, "s1", day365)
	if u.Day != 365 || u.AnnualUsed != 0 {
		t.Fatalf("after 365 days annual window must be free, got %+v", u)
	}
	// 此刻待审核汇款才被（逾期）拒绝，释放一个早已不在窗口的桶：
	// 年度额度恢复可用，提交新汇款成功。
	q2 := mustQuote(t, e, "s1", 1, 100000000, day365)
	r2 := mustSubmit(t, e, "s1", q2, "p2", "k2", day365)
	if r2.Status != StatusPending {
		t.Fatalf("annual budget should be reusable after 365-day roll, got %s", r2.Status)
	}
	info, _ := e.GetTransfer(res.TransferID, day365)
	if info.Status != StatusFailed || info.Day != 0 {
		t.Fatalf("old transfer failed with original day, got %+v", info)
	}
	u = mustUsage(t, e, "s1", day365)
	if u.AnnualUsed != 100 {
		t.Fatalf("only new transfer occupies current window, got %+v", u)
	}
}

// 被拒绝的提交不留痕：限额拒绝后报价与键可原样复用。
func TestRejectedSubmitLeavesNoTrace(t *testing.T) {
	e := testEngine()
	addTestSender(t, e, "s1", 1, 100, 100) // 单笔限额 1
	q := mustQuote(t, e, "s1", 10, 150000, 0)
	if _, err := e.Submit(SubmitRequest{"s1", q, "p", "k", 1}); CodeOf(err) != ErrCodeSingleLimitExceeded {
		t.Fatalf("want SingleLimitExceeded, got %v", err)
	}
	// 放开单笔限额（管理操作），同键、同报价立刻成功 => 报价未消耗、键未记录。
	e.AddSender("s1", Limits{Single: 100, Daily: 100, Annual: 100})
	res := mustSubmit(t, e, "s1", q, "p", "k", 2)
	if res.Status != StatusSucceeded || res.Occupied != 2 {
		t.Fatalf("want success occupied=2 after limit raised, got %+v", res)
	}
}

// 审核/撤回错误优先级：参数非法 > 时钟回退 > 不存在 > 状态不允许。
func TestReviewErrorPriority(t *testing.T) {
	e := testEngine()
	if err := e.Approve(0, 0); CodeOf(err) != ErrCodeInvalidArgument {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
	addTestSender(t, e, "s1", 100, 100, 100)
	mustQuote(t, e, "s1", 1, 100, 10)
	if err := e.Approve(999, 5); CodeOf(err) != ErrCodeClockBackward {
		t.Fatalf("clock check precedes existence, got %v", err)
	}
	if err := e.Approve(999, 10); CodeOf(err) != ErrCodeTransferNotFound {
		t.Fatalf("want TransferNotFound, got %v", err)
	}
}
