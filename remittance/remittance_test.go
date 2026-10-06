package remittance

import "testing"

func testEngine() *Engine {
	return New(Config{QuoteTTLSeconds: 100, ReviewSeconds: 60, ReviewThreshold: 1000})
}

func addTestSender(t *testing.T, e *Engine, id string, single, daily, annual int64) {
	t.Helper()
	e.AddSender(id, Limits{Single: single, Daily: daily, Annual: annual})
}

func mustQuote(t *testing.T, e *Engine, sender string, amount, rate, now int64) int64 {
	t.Helper()
	id, err := e.ApplyQuote(QuoteRequest{
		Sender: sender, SourceCCY: "USD", TargetCCY: "CNY",
		Amount: amount, RatePPM: rate, Now: now,
	})
	if err != nil {
		t.Fatalf("ApplyQuote sender=%s amount=%d rate=%d now=%d: %v", sender, amount, rate, now, err)
	}
	return id
}

func mustSubmit(t *testing.T, e *Engine, sender string, quoteID int64, payee, key string, now int64) SubmitResult {
	t.Helper()
	res, err := e.Submit(SubmitRequest{
		Sender: sender, QuoteID: quoteID, Payee: payee, IdemKey: key, Now: now,
	})
	if err != nil {
		t.Fatalf("Submit sender=%s quote=%d payee=%s key=%s now=%d: %v",
			sender, quoteID, payee, key, now, err)
	}
	return res
}

func mustUsage(t *testing.T, e *Engine, sender string, now int64) Usage {
	t.Helper()
	u, err := e.Usage(sender, now)
	if err != nil {
		t.Fatalf("Usage %s now=%d: %v", sender, now, err)
	}
	return u
}

// 报价到期恰等仍有效，晚一秒即过期；被拒提交不消耗报价。
func TestQuoteExpiryBoundary(t *testing.T) {
	e := testEngine()
	addTestSender(t, e, "s1", 1000, 1000, 1000)
	q := mustQuote(t, e, "s1", 1, 100, 0) // T=100，expiresAt=100

	if _, err := e.Submit(SubmitRequest{"s1", q, "p", "k1", 100}); err != nil {
		t.Fatalf("submit at exactly expiry second should be accepted: %v", err)
	}
	q2 := mustQuote(t, e, "s1", 1, 100, 200) // expiresAt=300
	if _, err := e.Submit(SubmitRequest{"s1", q2, "p", "k2", 301}); CodeOf(err) != ErrCodeQuoteExpired {
		t.Fatalf("one second after expiry want QuoteExpired, got %v", err)
	}
	res := mustSubmit(t, e, "s1", q2, "p", "k3", 300) // 拒绝未消耗报价
	if res.Status != StatusSucceeded {
		t.Fatalf("expired-then-valid submit should succeed, got %s", res.Status)
	}
}

// 目标额向下取整、占用额向上取整。
func TestFloorVsCeil(t *testing.T) {
	e := testEngine()
	addTestSender(t, e, "s1", 2, 100, 100)
	q := mustQuote(t, e, "s1", 10, 150000, 0) // 10*150000/1e6=1.5
	res := mustSubmit(t, e, "s1", q, "p", "k", 1)
	if res.TargetAmount != 1 || res.Occupied != 2 {
		t.Fatalf("want target=1 occupied=2, got target=%d occupied=%d",
			res.TargetAmount, res.Occupied)
	}
	if u := mustUsage(t, e, "s1", 1); u.DayUsed != 2 {
		t.Fatalf("day used must count ceiling occupied=2, got %d", u.DayUsed)
	}
}

// 占用额恰等单笔/日/年限额均视为满足。
func TestExactLimitSatisfied(t *testing.T) {
	e := testEngine()
	addTestSender(t, e, "s1", 5, 100, 100)    // 仅卡单笔，避免先触发日限额
	q := mustQuote(t, e, "s1", 10, 500000, 0) // product=5 恰等
	res := mustSubmit(t, e, "s1", q, "p", "k", 1)
	if res.Occupied != 5 {
		t.Fatalf("want occupied=5, got %d", res.Occupied)
	}
	q2 := mustQuote(t, e, "s1", 6, 1000000, 2) // occupied=6，超单笔 5
	if _, err := e.Submit(SubmitRequest{"s1", q2, "p", "k2", 2}); CodeOf(err) != ErrCodeSingleLimitExceeded {
		t.Fatalf("want SingleLimitExceeded, got %v", err)
	}
}

// 日限额先于年度额度报错。
func TestDailyThenAnnualPriority(t *testing.T) {
	e := testEngine()
	addTestSender(t, e, "s1", 100, 10, 1000)
	q := mustQuote(t, e, "s1", 6, 1000000, 0)
	mustSubmit(t, e, "s1", q, "p", "k1", 1)
	q2 := mustQuote(t, e, "s1", 6, 1000000, 2)
	if _, err := e.Submit(SubmitRequest{"s1", q2, "p", "k2", 2}); CodeOf(err) != ErrCodeDailyLimitExceeded {
		t.Fatalf("want DailyLimitExceeded, got %v", err)
	}

	e2 := testEngine()
	addTestSender(t, e2, "s1", 100, 1000, 10)
	q = mustQuote(t, e2, "s1", 6, 1000000, 0)
	mustSubmit(t, e2, "s1", q, "p", "k1", 1)
	q2 = mustQuote(t, e2, "s1", 6, 1000000, 2)
	if _, err := e2.Submit(SubmitRequest{"s1", q2, "p", "k2", 2}); CodeOf(err) != ErrCodeAnnualLimitExceeded {
		t.Fatalf("want AnnualLimitExceeded, got %v", err)
	}
}

// 时钟回退；被拒操作不推进时钟。
func TestClockBackward(t *testing.T) {
	e := testEngine()
	addTestSender(t, e, "s1", 100, 100, 100)
	mustQuote(t, e, "s1", 1, 100, 10)
	_, err := e.ApplyQuote(QuoteRequest{
		Sender: "s1", SourceCCY: "USD", TargetCCY: "CNY",
		Amount: 1, RatePPM: 100, Now: 9,
	})
	if CodeOf(err) != ErrCodeClockBackward {
		t.Fatalf("want ClockBackward, got %v", err)
	}
	mustQuote(t, e, "s1", 1, 100, 10) // 回退未生效，now=10 仍接受
}
