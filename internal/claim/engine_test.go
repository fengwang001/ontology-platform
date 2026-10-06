package claim

import (
	"errors"
	"testing"
)

func paidOf(t *testing.T, r *AccidentResult, pid string) int64 {
	t.Helper()
	for _, p := range r.Payments {
		if p.PolicyID == pid {
			return p.Amount
		}
	}
	return 0
}

func must(t *testing.T, err error, stage string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", stage, err)
	}
}

func mustRegister(t *testing.T, s *System, in Accident, stage string) *AccidentResult {
	t.Helper()
	r, err := s.RegisterAccident(in)
	must(t, err, stage)
	return r
}

func mustCorrect(t *testing.T, s *System, in CorrectAccidentInput, stage string) *AccidentResult {
	t.Helper()
	r, err := s.CorrectAccident(in)
	must(t, err, stage)
	return r
}

func mustResult(t *testing.T, s *System, id, stage string) *AccidentResult {
	t.Helper()
	r, err := s.AccidentResult(id)
	must(t, err, stage)
	return r
}

// 免赔额恰等损失额：独立赔付额为 0，S=0，不赔。
func TestDeductibleEqualsLoss(t *testing.T) {
	s := NewSystem()
	must(t, s.AddPolicy(Policy{ID: "P1", Subject: "car", Limit: 1000, Deductible: 300, StartDay: 0, EndDay: 10, Insurer: "A"}), "add")
	r := mustRegister(t, s, Accident{ID: "A1", Subject: "car", Day: 5, Loss: 300}, "register")
	if r.TotalPaid != 0 || len(r.Payments) != 0 {
		t.Fatalf("loss==deductible must pay zero, got %+v", r)
	}
	t.Logf("input loss=300 deductible=300 -> independent=max(300-300,0)=0, S=0 => %+v", r)
}

// 剩余保额恰好耗尽：累计赔付 == 保额，之后事故再无赔付。
func TestLimitExactlyExhausted(t *testing.T) {
	s := NewSystem()
	must(t, s.AddPolicy(Policy{ID: "P1", Subject: "x", Limit: 500, Deductible: 0, StartDay: 0, EndDay: 100, Insurer: "A"}), "add")
	r1 := mustRegister(t, s, Accident{ID: "A1", Subject: "x", Day: 1, Loss: 500}, "r1")
	if paidOf(t, r1, "P1") != 500 {
		t.Fatalf("first claim must exhaust 500, got %d", r1.TotalPaid)
	}
	r2 := mustRegister(t, s, Accident{ID: "A2", Subject: "x", Day: 2, Loss: 500}, "r2")
	if r2.TotalPaid != 0 {
		t.Fatalf("exhausted policy must pay 0, got %d", r2.TotalPaid)
	}
	rem, _ := s.RemainingLimit("P1")
	if rem != 0 {
		t.Fatalf("remaining must be 0, got %d", rem)
	}
	t.Logf("A1 consumes full limit 500; A2 remaining=0, independent capped at 0 => %+v", r2)
}

// 取整余数分配次序：份额向下取整后，余数按 (生效日, 编号) 补给未足额者。
func TestRemainderOrdering(t *testing.T) {
	s := NewSystem()
	must(t, s.AddPolicy(Policy{ID: "Pb", Subject: "x", Limit: 100, Deductible: 0, StartDay: 2, EndDay: 9, Insurer: "B"}), "add")
	must(t, s.AddPolicy(Policy{ID: "Pa2", Subject: "x", Limit: 100, Deductible: 0, StartDay: 1, EndDay: 9, Insurer: "A2"}), "add")
	must(t, s.AddPolicy(Policy{ID: "Pa1", Subject: "x", Limit: 100, Deductible: 0, StartDay: 1, EndDay: 9, Insurer: "A1"}), "add")
	r := mustRegister(t, s, Accident{ID: "A1", Subject: "x", Day: 5, Loss: 100}, "register")
	if paidOf(t, r, "Pa1") != 34 || paidOf(t, r, "Pa2") != 33 || paidOf(t, r, "Pb") != 33 {
		t.Fatalf("remainder must go to (start=1,id=Pa1), got Pa1=%d Pa2=%d Pb=%d",
			paidOf(t, r, "Pa1"), paidOf(t, r, "Pa2"), paidOf(t, r, "Pb"))
	}
	t.Logf("base 33/33/33, remainder 1 to earliest (start,id)=Pa1: %+v", r.Payments)
}

// 普通保单足额赔付时，超额保单不介入。
func TestExcessStaysOutWhenOrdinaryCoversLoss(t *testing.T) {
	s := NewSystem()
	must(t, s.AddPolicy(Policy{ID: "O1", Subject: "x", Limit: 1000, Deductible: 0, StartDay: 0, EndDay: 10, Clause: Ordinary}), "add o")
	must(t, s.AddPolicy(Policy{ID: "E1", Subject: "x", Limit: 1000, Deductible: 0, StartDay: 0, EndDay: 10, Clause: Excess}), "add e")
	r := mustRegister(t, s, Accident{ID: "A1", Subject: "x", Day: 5, Loss: 400}, "register")
	if paidOf(t, r, "O1") != 400 || paidOf(t, r, "E1") != 0 {
		t.Fatalf("ordinary must cover full loss, excess zero, got %+v", r)
	}
	t.Logf("ordinary pays 400 == loss, uncompensated=0, excess E1 makes no payment")
}

// 超额保单在普通保单赔不足时介入并受独立赔付额限制。
func TestExcessStepsIn(t *testing.T) {
	s := NewSystem()
	must(t, s.AddPolicy(Policy{ID: "O1", Subject: "x", Limit: 100, Deductible: 0, StartDay: 0, EndDay: 10, Clause: Ordinary}), "add o")
	must(t, s.AddPolicy(Policy{ID: "E1", Subject: "x", Limit: 1000, Deductible: 0, StartDay: 0, EndDay: 10, Clause: Excess}), "add e")
	r := mustRegister(t, s, Accident{ID: "A1", Subject: "x", Day: 5, Loss: 300}, "register")
	if paidOf(t, r, "O1") != 100 || paidOf(t, r, "E1") != 200 || r.TotalPaid != 300 {
		t.Fatalf("want 100 ordinary + 200 excess, got %+v", r)
	}
	t.Logf("ordinary cap 100, uncompensated 200 paid by excess: %+v", r.Payments)
}

// 更正使后续事故结果连锁变化：降低 A1 损失释放限额，A2 随之可多赔。
func TestCorrectionCascades(t *testing.T) {
	s := NewSystem()
	must(t, s.AddPolicy(Policy{ID: "P1", Subject: "x", Limit: 100, Deductible: 0, StartDay: 0, EndDay: 100, Insurer: "A"}), "add")
	r1 := mustRegister(t, s, Accident{ID: "A1", Subject: "x", Day: 1, Loss: 100}, "r1")
	r2 := mustRegister(t, s, Accident{ID: "A2", Subject: "x", Day: 2, Loss: 100}, "r2")
	if r1.TotalPaid != 100 || r2.TotalPaid != 0 {
		t.Fatalf("initial state wrong: %d %d", r1.TotalPaid, r2.TotalPaid)
	}
	r1c := mustCorrect(t, s, CorrectAccidentInput{AccidentID: "A1", NewLoss: 60}, "correct")
	if r1c.TotalPaid != 60 {
		t.Fatalf("corrected A1 must pay 60, got %d", r1c.TotalPaid)
	}
	r2c := mustResult(t, s, "A2", "r2 after")
	if r2c.TotalPaid != 40 {
		t.Fatalf("A2 must chain-change to 40, got %d", r2c.TotalPaid)
	}
	if !s.ReplayConsistencyOK() {
		t.Fatalf("incremental state diverges from full replay")
	}
	rem, _ := s.RemainingLimit("P1")
	if rem != 0 {
		t.Fatalf("limit stays fully consumed, got %d", rem)
	}
	t.Logf("A1 corrected 100->60 frees 40; A2 recomputed to 40; full replay agrees")
}

// 注销日恰等事故发生日：历史事故不受影响；当天及以后新登记事故不再受覆盖。
func TestCancelDayEqualsAccidentDay(t *testing.T) {
	s := NewSystem()
	must(t, s.AddPolicy(Policy{ID: "P1", Subject: "x", Limit: 1000, Deductible: 0, StartDay: 0, EndDay: 100, Insurer: "A"}), "add")
	r1 := mustRegister(t, s, Accident{ID: "A1", Subject: "x", Day: 10, Loss: 100}, "r1")
	if r1.TotalPaid != 100 {
		t.Fatalf("A1 must pay 100, got %d", r1.TotalPaid)
	}
	must(t, s.CancelPolicy(CancelPolicyInput{PolicyID: "P1", CancelDay: 10}), "cancel")
	r2 := mustRegister(t, s, Accident{ID: "A2", Subject: "x", Day: 10, Loss: 100}, "r2")
	if r2.TotalPaid != 0 {
		t.Fatalf("new accident on cancel day must be uncovered, got %d", r2.TotalPaid)
	}
	r1b := mustResult(t, s, "A1", "history")
	if r1b.TotalPaid != 100 {
		t.Fatalf("history must not change, got %d", r1b.TotalPaid)
	}
	t.Logf("cancel effective day 10: A1(day10) stays 100; later A2(day10) uncovered -> 0")
}

// 注销日早于已覆盖过的事故发生日 => 参数非法；已注销再注销 => 已注销。
func TestCancelValidation(t *testing.T) {
	s := NewSystem()
	must(t, s.AddPolicy(Policy{ID: "P1", Subject: "x", Limit: 1000, Deductible: 0, StartDay: 0, EndDay: 100, Insurer: "A"}), "add")
	mustRegister(t, s, Accident{ID: "A1", Subject: "x", Day: 20, Loss: 50}, "register")
	if err := s.CancelPolicy(CancelPolicyInput{PolicyID: "P1", CancelDay: 19}); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("want ErrInvalidArg, got %v", err)
	}
	must(t, s.CancelPolicy(CancelPolicyInput{PolicyID: "P1", CancelDay: 20}), "cancel20")
	if err := s.CancelPolicy(CancelPolicyInput{PolicyID: "P1", CancelDay: 21}); !errors.Is(err, ErrPolicyCancelled) {
		t.Fatalf("want ErrPolicyCancelled, got %v", err)
	}
}

// 无覆盖保单的事故登记成功且赔付为零。
func TestNoCoveragePaysZero(t *testing.T) {
	s := NewSystem()
	r := mustRegister(t, s, Accident{ID: "A1", Subject: "ghost", Day: 1, Loss: 999}, "register")
	if r.TotalPaid != 0 || r.Seq != 0 {
		t.Fatalf("uncovered accident must succeed with zero payout, got %+v", r)
	}
	t.Logf("no covering policy: registration succeeds, seq=0, payout=0")
}

// 被拒绝操作不留痕，且错误优先级 参数非法 > 重复/不存在 > 已注销。
func TestRejectedOperationsLeaveNoTrace(t *testing.T) {
	s := NewSystem()
	must(t, s.AddPolicy(Policy{ID: "P1", Subject: "x", Limit: 100, Deductible: 0, StartDay: 0, EndDay: 10, Insurer: "A"}), "add")
	base := mustRegister(t, s, Accident{ID: "A1", Subject: "x", Day: 5, Loss: 80}, "register")

	if err := s.AddPolicy(Policy{ID: "BAD", Subject: "x", Limit: 0, Deductible: 0, StartDay: 0, EndDay: 1}); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("want invalid, got %v", err)
	}
	if err := s.AddPolicy(Policy{ID: "BAD", Subject: "x", Limit: 1, Deductible: 0, StartDay: 9, EndDay: 1}); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("want invalid, got %v", err)
	}
	if err := s.AddPolicy(Policy{ID: "P1", Subject: "y", Limit: 1, Deductible: 0, StartDay: 0, EndDay: 1}); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("want duplicate, got %v", err)
	}
	if _, err := s.RegisterAccident(Accident{ID: "A1", Subject: "x", Day: 5, Loss: 1}); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("want duplicate accident, got %v", err)
	}
	if _, err := s.CorrectAccident(CorrectAccidentInput{AccidentID: "NOPE", NewLoss: 1}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	if err := s.CancelPolicy(CancelPolicyInput{PolicyID: "NOPE", CancelDay: 1}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	if _, err := s.CorrectAccident(CorrectAccidentInput{AccidentID: "NOPE", NewLoss: -1}); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("invalid must precede not-found, got %v", err)
	}
	if err := s.CancelPolicy(CancelPolicyInput{PolicyID: "", CancelDay: -1}); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("invalid must precede not-found, got %v", err)
	}

	after := mustResult(t, s, "A1", "reread")
	if after.TotalPaid != base.TotalPaid || len(after.Payments) != len(base.Payments) {
		t.Fatalf("rejected ops changed state: before %+v after %+v", base, after)
	}
	rem, _ := s.RemainingLimit("P1")
	if rem != 20 {
		t.Fatalf("remaining must stay 20 after rejected ops, got %d", rem)
	}
	t.Logf("all rejected operations validated before mutation; result unchanged %+v, remaining=20", after)
}
