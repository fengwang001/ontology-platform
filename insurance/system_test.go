package insurance

import (
	"reflect"
	"testing"
)

func mustAddPolicy(t *testing.T, s *System, p Policy) {
	t.Helper()
	if err := s.AddPolicy(p); err != nil {
		t.Fatalf("AddPolicy(%s) failed: %v", p.ID, err)
	}
}

func mustRegister(t *testing.T, s *System, a Accident) AccidentResult {
	t.Helper()
	res, err := s.RegisterAccident(a)
	if err != nil {
		t.Fatalf("RegisterAccident(%s) failed: %v", a.ID, err)
	}
	return res
}

func payoutOf(res AccidentResult, policyID string) int64 {
	for _, p := range res.Payouts {
		if p.PolicyID == policyID {
			return p.Amount
		}
	}
	return 0
}

func checkKind(t *testing.T, err error, want ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error kind %s, got nil", want)
	}
	got, ok := KindOf(err)
	if !ok {
		t.Fatalf("expected *Error, got %T (%v)", err, err)
	}
	if got != want {
		t.Fatalf("expected error kind %s, got %s (%v)", want, got, err)
	}
}

// 免赔额恰等损失额：独立赔付额为零，不赔。
func TestDeductibleEqualsLoss(t *testing.T) {
	s := NewSystem()
	mustAddPolicy(t, s, Policy{ID: "P1", Subject: "car", SumInsured: 1000, Deductible: 500,
		Effective: 0, Expiry: 100, Insurer: "ins", Clause: ClauseNormal})
	res := mustRegister(t, s, Accident{ID: "A1", Subject: "car", Date: 10, Loss: 500})
	if res.TotalPaid != 0 || payoutOf(res, "P1") != 0 {
		t.Fatalf("deductible == loss must pay 0, got %+v", res)
	}
	st, _ := s.PolicyStatusOf("P1")
	if st.Remaining != 1000 {
		t.Fatalf("remaining must stay 1000, got %d", st.Remaining)
	}
}

// 剩余保额恰好耗尽：赔付后剩余为零，后续事故该保单不再赔付。
func TestRemainingExactlyExhausted(t *testing.T) {
	s := NewSystem()
	mustAddPolicy(t, s, Policy{ID: "P1", Subject: "car", SumInsured: 100, Deductible: 0,
		Effective: 0, Expiry: 100, Insurer: "ins", Clause: ClauseNormal})
	res1 := mustRegister(t, s, Accident{ID: "A1", Subject: "car", Date: 1, Loss: 100})
	if res1.TotalPaid != 100 {
		t.Fatalf("first accident must pay 100, got %d", res1.TotalPaid)
	}
	st, _ := s.PolicyStatusOf("P1")
	if st.Remaining != 0 {
		t.Fatalf("remaining must be exactly 0, got %d", st.Remaining)
	}
	res2 := mustRegister(t, s, Accident{ID: "A2", Subject: "car", Date: 2, Loss: 50})
	if res2.TotalPaid != 0 {
		t.Fatalf("exhausted policy must pay 0, got %d", res2.TotalPaid)
	}
}

// 取整余数的分配次序：按生效日由早到晚、再按编号由小到大逐单位补发。
func TestRemainderDistributionOrder(t *testing.T) {
	s := NewSystem()
	// 注册顺序故意与余数分配顺序不同。
	mustAddPolicy(t, s, Policy{ID: "Pc", Subject: "car", SumInsured: 100, Effective: 3, Expiry: 100, Insurer: "i", Clause: ClauseNormal})
	mustAddPolicy(t, s, Policy{ID: "Pb", Subject: "car", SumInsured: 100, Effective: 1, Expiry: 100, Insurer: "i", Clause: ClauseNormal})
	mustAddPolicy(t, s, Policy{ID: "Pa", Subject: "car", SumInsured: 100, Effective: 1, Expiry: 100, Insurer: "i", Clause: ClauseNormal})
	// 独立赔付额各 100，S=300，总赔付=min(300,101)=101。
	// 份额 floor(100*101/300)=33，余数 2：先生效日 1 的 Pa、Pb 各补 1。
	res := mustRegister(t, s, Accident{ID: "A1", Subject: "car", Date: 10, Loss: 101})
	want := map[string]int64{"Pa": 34, "Pb": 34, "Pc": 33}
	for id, w := range want {
		if got := payoutOf(res, id); got != w {
			t.Fatalf("policy %s: want %d, got %d (res=%+v)", id, w, got, res)
		}
	}
	if res.TotalPaid != 101 {
		t.Fatalf("total must be 101, got %d", res.TotalPaid)
	}
}

// 普通保单足额赔付时超额保单不介入；不足时超额保单只补未补偿部分。
func TestExcessOnlyAfterNormal(t *testing.T) {
	s := NewSystem()
	mustAddPolicy(t, s, Policy{ID: "Pn", Subject: "car", SumInsured: 100, Effective: 0, Expiry: 100, Insurer: "i", Clause: ClauseNormal})
	mustAddPolicy(t, s, Policy{ID: "Pe", Subject: "car", SumInsured: 100, Effective: 0, Expiry: 100, Insurer: "i", Clause: ClauseExcess})
	res1 := mustRegister(t, s, Accident{ID: "A1", Subject: "car", Date: 1, Loss: 80})
	if res1.TotalPaid != 80 || payoutOf(res1, "Pe") != 0 {
		t.Fatalf("normal fully covers: excess must not pay, got %+v", res1)
	}
	res2 := mustRegister(t, s, Accident{ID: "A2", Subject: "car", Date: 2, Loss: 50})
	// 普通剩余 20，未补偿 30，超额独立赔付额 50 → 赔 30。
	if payoutOf(res2, "Pn") != 20 || payoutOf(res2, "Pe") != 30 || res2.TotalPaid != 50 {
		t.Fatalf("excess must top up uncompensated loss, got %+v", res2)
	}
}

// 更正使后续事故结果连锁变化，且剩余保额与从头重放一致。
func TestCorrectionCascade(t *testing.T) {
	s := NewSystem()
	mustAddPolicy(t, s, Policy{ID: "P1", Subject: "car", SumInsured: 100, Effective: 0, Expiry: 100, Insurer: "i", Clause: ClauseNormal})
	mustAddPolicy(t, s, Policy{ID: "P2", Subject: "car", SumInsured: 50, Effective: 0, Expiry: 100, Insurer: "i", Clause: ClauseExcess})
	mustRegister(t, s, Accident{ID: "A1", Subject: "car", Date: 1, Loss: 80})
	res2 := mustRegister(t, s, Accident{ID: "A2", Subject: "car", Date: 2, Loss: 60})
	if payoutOf(res2, "P1") != 20 || payoutOf(res2, "P2") != 40 {
		t.Fatalf("before correction: %+v", res2)
	}
	if err := s.CorrectAccident("A1", 50); err != nil {
		t.Fatalf("correct failed: %v", err)
	}
	r1, _ := s.AccidentResultOf("A1")
	r2, _ := s.AccidentResultOf("A2")
	if r1.Loss != 50 || r1.TotalPaid != 50 {
		t.Fatalf("A1 after correction: %+v", r1)
	}
	if payoutOf(r2, "P1") != 50 || payoutOf(r2, "P2") != 10 || r2.TotalPaid != 60 {
		t.Fatalf("A2 must cascade: %+v", r2)
	}
	st1, _ := s.PolicyStatusOf("P1")
	st2, _ := s.PolicyStatusOf("P2")
	if st1.TotalPaid != 100 || st1.Remaining != 0 || st2.TotalPaid != 10 || st2.Remaining != 40 {
		t.Fatalf("remaining must match full replay: P1=%+v P2=%+v", st1, st2)
	}
}

// 注销日恰等事故发生日：已覆盖该事故时报参数非法；
// 先注销则当日及以后的事故不再受覆盖。
func TestCancelDateEqualsAccidentDate(t *testing.T) {
	s := NewSystem()
	mustAddPolicy(t, s, Policy{ID: "P1", Subject: "car", SumInsured: 100, Effective: 0, Expiry: 100, Insurer: "i", Clause: ClauseNormal})
	mustRegister(t, s, Accident{ID: "A1", Subject: "car", Date: 5, Loss: 10})
	err := s.CancelPolicy("P1", 5, "i")
	checkKind(t, err, ErrKindInvalidParam)
	if err := s.CancelPolicy("P1", 6, "i"); err != nil {
		t.Fatalf("cancel at day 6 must succeed: %v", err)
	}
	res := mustRegister(t, s, Accident{ID: "A2", Subject: "car", Date: 6, Loss: 10})
	if res.TotalPaid != 0 {
		t.Fatalf("accident on/after cancel date must not be covered: %+v", res)
	}
	res2 := mustRegister(t, s, Accident{ID: "A3", Subject: "car", Date: 5, Loss: 10})
	if res2.TotalPaid != 10 {
		t.Fatalf("accident before cancel date must still be covered: %+v", res2)
	}
}

// 晚登记的保单不得追溯参与先登记的事故，即使重算。
func TestLatePolicyNotRetroactive(t *testing.T) {
	s := NewSystem()
	res := mustRegister(t, s, Accident{ID: "A1", Subject: "car", Date: 5, Loss: 10})
	if res.TotalPaid != 0 {
		t.Fatalf("no covering policy: %+v", res)
	}
	mustAddPolicy(t, s, Policy{ID: "P1", Subject: "car", SumInsured: 100, Effective: 0, Expiry: 100, Insurer: "i", Clause: ClauseNormal})
	if err := s.CorrectAccident("A1", 20); err != nil {
		t.Fatalf("correct failed: %v", err)
	}
	r, _ := s.AccidentResultOf("A1")
	if r.TotalPaid != 0 || len(r.Payouts) != 0 {
		t.Fatalf("late policy must not join recomputation: %+v", r)
	}
	st, _ := s.PolicyStatusOf("P1")
	if st.TotalPaid != 0 {
		t.Fatalf("late policy must not pay: %+v", st)
	}
}

// 无覆盖保单的事故登记成功且赔付为零。
func TestAccidentWithoutCoverageSucceeds(t *testing.T) {
	s := NewSystem()
	mustAddPolicy(t, s, Policy{ID: "P1", Subject: "house", SumInsured: 100, Effective: 0, Expiry: 100, Insurer: "i", Clause: ClauseNormal})
	res := mustRegister(t, s, Accident{ID: "A1", Subject: "car", Date: 1, Loss: 50})
	if res.TotalPaid != 0 || len(res.Payouts) != 0 {
		t.Fatalf("uncovered accident must pay 0: %+v", res)
	}
}

// 被拒绝的操作不留痕。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	s := NewSystem()
	mustAddPolicy(t, s, Policy{ID: "P1", Subject: "car", SumInsured: 100, Effective: 0, Expiry: 100, Insurer: "i", Clause: ClauseNormal})
	mustRegister(t, s, Accident{ID: "A1", Subject: "car", Date: 1, Loss: 30})
	beforeRes, _ := s.AccidentResultOf("A1")
	beforeSt, _ := s.PolicyStatusOf("P1")
	beforeStats := s.Stats()

	// 各类被拒绝操作。
	checkKind(t, s.AddPolicy(Policy{ID: "P2", Subject: "car", SumInsured: 0, Effective: 0, Expiry: 1, Insurer: "i"}), ErrKindInvalidParam)
	checkKind(t, s.AddPolicy(Policy{ID: "P1", Subject: "car", SumInsured: 5, Effective: 0, Expiry: 1, Insurer: "i"}), ErrKindDuplicateID)
	if _, err := s.RegisterAccident(Accident{ID: "A1", Subject: "car", Date: 2, Loss: 1}); true {
		checkKind(t, err, ErrKindDuplicateID)
	}
	checkKind(t, s.CorrectAccident("nope", 10), ErrKindNotFound)
	checkKind(t, s.CancelPolicy("nope", 10, "i"), ErrKindNotFound)
	checkKind(t, s.CancelPolicy("P1", 1, "i"), ErrKindInvalidParam) // 注销日须晚于已覆盖事故日 1
	if err := s.CancelPolicy("P1", 10, "i"); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	checkKind(t, s.CancelPolicy("P1", 20, "i"), ErrKindAlreadyCancelled)

	afterRes, _ := s.AccidentResultOf("A1")
	afterSt, _ := s.PolicyStatusOf("P1")
	if !reflect.DeepEqual(beforeRes, afterRes) {
		t.Fatalf("rejected ops changed accident result: %+v -> %+v", beforeRes, afterRes)
	}
	if afterSt.TotalPaid != beforeSt.TotalPaid || afterSt.Remaining != beforeSt.Remaining {
		t.Fatalf("rejected ops changed policy state: %+v -> %+v", beforeSt, afterSt)
	}
	if _, ok := s.PolicyStatusOf("P2"); ok {
		t.Fatalf("rejected AddPolicy left a trace")
	}
	if _, ok := s.AccidentResultOf("nope"); ok {
		t.Fatalf("rejected accident op left a trace")
	}
	_ = beforeStats
}

// 错误优先级：参数非法 > 编号重复 > 不存在 > 已注销。
func TestErrorPriority(t *testing.T) {
	s := NewSystem()
	mustAddPolicy(t, s, Policy{ID: "P1", Subject: "car", SumInsured: 100, Effective: 0, Expiry: 100, Insurer: "i", Clause: ClauseNormal})
	// 非法 + 重复 → 非法。
	checkKind(t, s.AddPolicy(Policy{ID: "P1", Subject: "car", SumInsured: -1, Effective: 0, Expiry: 1, Insurer: "i"}), ErrKindInvalidParam)
	// 非法 + 重复（事故）→ 非法。
	mustRegister(t, s, Accident{ID: "A1", Subject: "car", Date: 1, Loss: 1})
	_, err := s.RegisterAccident(Accident{ID: "A1", Subject: "car", Date: -1, Loss: 1})
	checkKind(t, err, ErrKindInvalidParam)
	// 非法 + 不存在（更正）→ 非法。
	checkKind(t, s.CorrectAccident("nope", -5), ErrKindInvalidParam)
	// 非法 + 不存在（注销）→ 非法。
	checkKind(t, s.CancelPolicy("nope", -1, "i"), ErrKindInvalidParam)
	// 不存在（注销）→ 不存在。
	checkKind(t, s.CancelPolicy("nope", 1, "i"), ErrKindNotFound)
	// 已注销 vs 非法保险人：参数非法优先。
	if err := s.CancelPolicy("P1", 10, "i"); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	checkKind(t, s.CancelPolicy("P1", 20, "other"), ErrKindInvalidParam)
	checkKind(t, s.CancelPolicy("P1", 20, "i"), ErrKindAlreadyCancelled)
}
