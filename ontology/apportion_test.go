package ontology

import (
	"testing"
)

func mkState(p Policy) *policyState {
	return &policyState{spec: p, remains: p.AnnualLimit}
}

func pol(id string, clause Clause, ded, per, annual int64, from, to int) Policy {
	return Policy{
		Insured:      "I",
		PolicyID:     id,
		Deductible:   ded,
		PerLossLimit: per,
		AnnualLimit:  annual,
		CoverFrom:    from,
		CoverTo:      to,
		Clause:       clause,
	}
}

// 损失日恰等于承保区间右端不参与；年度累计恰好耗尽的保单不入分母。
func TestCoverRightEndExcludedAndExhausted(t *testing.T) {
	e := NewEngine()
	must(t, e.RegisterPolicy(pol("P1", ClauseLimitProportional, 0, 1000, 10000, 0, 10)))
	must(t, e.RegisterPolicy(pol("P2", ClauseLimitProportional, 0, 1000, 500, 0, 10)))
	must(t, e.RegisterPolicy(pol("P3", ClauseLimitProportional, 0, 1000, 1000, 0, 5)))

	// 第一笔 day=5：P3 右端=5 不参与；P1/P2 按限额 1000:1000 各得 250，
	// 权重为 min(每次限额, 年度剩余)=1000:500，损失 1500：P1=1000、P2=500，P2 年度恰好耗尽。
	r1, err := e.AcceptLoss(Loss{LossID: "L1", Insured: "I", Day: 5, Amount: 1500})
	mustOK(t, r1, err)
	if r1.Shares["P1"] != 1000 || r1.Shares["P2"] != 500 {
		t.Fatalf("L1 shares = %v", r1.Shares)
	}
	// 第二笔 day=6：P3 仍不参与，P2 年度恰好耗尽不入分母；只剩 P1 全赔。
	r2, err := e.AcceptLoss(Loss{LossID: "L2", Insured: "I", Day: 6, Amount: 300})
	mustOK(t, r2, err)
	if r2.Shares["P1"] != 300 || r2.Shares["P2"] != 0 || r2.Shares["P3"] != 0 {
		t.Fatalf("L2 shares = %v", r2.Shares)
	}
	b, _, _ := e.Balance("I", "P2")
	if b != 0 {
		t.Fatalf("P2 balance = %d, want 0", b)
	}
}

// 纯限额比例型中某张先达独立责任额后余额在其余保单间再分配。
func TestPureLimitProportionalRecascade(t *testing.T) {
	states := []*policyState{
		mkState(pol("A", ClauseLimitProportional, 900, 1000, 10000, 0, 100)),
		mkState(pol("B", ClauseLimitProportional, 0, 1000, 10000, 0, 100)),
		mkState(pol("C", ClauseLimitProportional, 0, 2000, 5000, 0, 100)),
	}
	r := apportion(states, 1, 1000)
	// A 先按比例得 250 但其独立责任额仅 100（免赔 900）-> 封顶 100；
	// 余额 900 在 B(权重1000)、C(权重2000) 间按比例：B 300、C 600，恰好无尾差。
	if r.Stage1.Mode != "限额比例" || r.Shares["A"] != 100 ||
		r.Shares["B"] != 300 || r.Shares["C"] != 600 || r.Paid != 1000 {
		t.Fatalf("shares = %v paid = %d", r.Shares, r.Paid)
	}
}

// 混入一张独立责任型后整体改按独立责任比例。
func TestIndependentSwitchesMode(t *testing.T) {
	states := []*policyState{
		mkState(pol("A", ClauseLimitProportional, 0, 1000, 5000, 0, 100)),
		mkState(pol("B", ClauseLimitProportional, 500, 10000, 50000, 0, 100)),
		mkState(pol("C", ClauseIndependentLiability, 0, 10000, 50000, 0, 100)),
	}
	r := apportion(states, 1, 1000)
	if r.Stage1.Mode != "独立责任比例" {
		t.Fatalf("mode = %s", r.Stage1.Mode)
	}
	// 独立责任额 A=1000, B=500, C=1000 => 400/200/400
	if r.Shares["A"] != 400 || r.Shares["B"] != 200 || r.Shares["C"] != 400 {
		t.Fatalf("shares = %v", r.Shares)
	}
}

// 超额型仅在非超额型未赔足时参与；非超额已赔足时超额不动。
func TestExcessOnlyWhenShortfall(t *testing.T) {
	mk := func(excessDed int64) ApportionResult {
		states := []*policyState{
			mkState(pol("P", ClauseLimitProportional, 0, 400, 5000, 0, 100)),
			mkState(pol("X", ClauseExcess, excessDed, 10000, 50000, 0, 100)),
		}
		return apportion(states, 1, 1000)
	}
	r := mk(0)
	if r.Shares["P"] != 400 || r.Shares["X"] != 600 || r.Paid != 1000 {
		t.Fatalf("shortfall shares = %v", r.Shares)
	}
	// 非超额已赔足：P 限额 2000 >= 损失 1000，X 不参与。
	full := apportion([]*policyState{
		mkState(pol("P", ClauseLimitProportional, 0, 2000, 5000, 0, 100)),
		mkState(pol("X", ClauseExcess, 0, 10000, 50000, 0, 100)),
	}, 1, 1000)
	if full.Shares["P"] != 1000 || full.Shares["X"] != 0 {
		t.Fatalf("covered shares = %v", full.Shares)
	}
}

// 向下取整尾差：并列时给编号字典序最小者；不并列时给独立责任额最大者。
func TestRoundingTieBreak(t *testing.T) {
	// 100 由三张等独立责任额保单分摊：33/33/33，尾差 1 分给编号最小 A。
	r := apportion([]*policyState{
		mkState(pol("C", ClauseIndependentLiability, 0, 100, 5000, 0, 100)),
		mkState(pol("A", ClauseIndependentLiability, 0, 100, 5000, 0, 100)),
		mkState(pol("B", ClauseIndependentLiability, 0, 100, 5000, 0, 100)),
	}, 1, 100)
	if r.Shares["A"] != 34 || r.Shares["B"] != 33 || r.Shares["C"] != 33 {
		t.Fatalf("tie shares = %v", r.Shares)
	}
	// 10 按独立责任 6:3:1 => 6/3/1 整除无尾差；改为 6:3:0 时尾差归最大者。
	r2 := apportion([]*policyState{
		mkState(pol("A", ClauseIndependentLiability, 0, 6, 5000, 0, 100)),
		mkState(pol("B", ClauseIndependentLiability, 0, 4, 5000, 0, 100)),
	}, 1, 9)
	// floor: 9*6/10=5, 9*4/10=3，尾差 1：A 的 cap 6 最大 -> A。
	if r2.Shares["A"] != 6 || r2.Shares["B"] != 3 {
		t.Fatalf("largest-cap shares = %v", r2.Shares)
	}
}

// 合计封顶：恰等于损失金额，以及各保单独立责任额之和。
func TestCapsAtLossAndAtSumIndependent(t *testing.T) {
	// 两张保单独立责任合计 600 < 损失 1000：合计封顶为 600。
	r := apportion([]*policyState{
		mkState(pol("A", ClauseLimitProportional, 0, 300, 5000, 0, 100)),
		mkState(pol("B", ClauseLimitProportional, 0, 300, 5000, 0, 100)),
	}, 1, 1000)
	if r.Paid != 600 || r.Shares["A"] != 300 || r.Shares["B"] != 300 {
		t.Fatalf("sum-cap shares = %v paid = %d", r.Shares, r.Paid)
	}
	// 独立责任合计 2000 > 损失 1000：合计恰等于损失。
	r2 := apportion([]*policyState{
		mkState(pol("A", ClauseLimitProportional, 0, 1000, 5000, 0, 100)),
		mkState(pol("B", ClauseLimitProportional, 0, 1000, 5000, 0, 100)),
	}, 1, 1000)
	if r2.Paid != 1000 {
		t.Fatalf("loss-cap paid = %d", r2.Paid)
	}
}

// 无参与保单：「无可赔保单」。
func TestNoPayer(t *testing.T) {
	r := apportion(nil, 1, 100)
	if !r.NoPayer || r.Paid != 0 {
		t.Fatalf("want no payer, got %+v", r)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustOK(t *testing.T, r *ApportionResult, err error) {
	t.Helper()
	must(t, err)
	if r == nil {
		t.Fatal("nil result")
	}
}
