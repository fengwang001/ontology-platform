package apportion

import "testing"

func mkPart(no string, deductible, perLoss, annual, remaining int64, c Clause) *participant {
	p := &Policy{
		Insured: "I", PolicyNo: no, Deductible: deductible, PerLoss: perLoss,
		AnnualLimit: annual, StartDay: 0, EndDay: 100, Clause: c,
	}
	return &participant{policy: p, remaining: remaining}
}

func paysOf(d *decision) map[string]int64 {
	m := map[string]int64{}
	for _, s := range d.allStages() {
		for _, r := range s.policies {
			m[r.policyNo] += r.pay
		}
	}
	return m
}

// 纯限额比例：A 先达独立责任额，余额在其余保单间再分配。
func TestLimitShareRedistribution(t *testing.T) {
	// 损失 1000。A: 免赔900 限额200 => IL=100，但限额权重=200；
	// 按 200:500:500 比例 A 应得 166 > 100，先达顶，余 900 在 B/C 间再分。
	a := mkPart("A", 900, 200, 1000, 1000, LimitShare)
	b := mkPart("B", 0, 500, 1000, 1000, LimitShare)
	c := mkPart("C", 0, 500, 1000, 1000, LimitShare)
	for _, p := range []*participant{a, b, c} {
		p.il = independentLiability(p.policy, p.remaining, 1000)
	}
	d := settle([]*participant{a, b, c}, 1000)
	got := paysOf(d)
	if got["A"] != 100 || got["B"] != 450 || got["C"] != 450 {
		t.Fatalf("got %v want A=100 B=450 C=450", got)
	}
}

// 混入一张独立责任型：整体改按独立责任额比例。
func TestIndependentModeSwitch(t *testing.T) {
	// 损失 1000。A: IL=1000；B: IL=250（限额型）；C 独立责任型 IL=250。
	a := mkPart("A", 0, 1000, 1000, 1000, LimitShare)
	b := mkPart("B", 750, 1000, 1000, 1000, LimitShare)
	cc := mkPart("C", 750, 1000, 1000, 1000, IndependentShare)
	for _, p := range []*participant{a, b, cc} {
		p.il = independentLiability(p.policy, p.remaining, 1000)
	}
	d := settle([]*participant{a, b, cc}, 1000)
	got := paysOf(d)
	// 1000:250:250 => A=666.67 floor, B/C=166.67；尾差 2 分给 IL 最大的 A 两次。
	if got["A"] != 668 || got["B"] != 166 || got["C"] != 166 {
		t.Fatalf("got %v want A=668 B=166 C=166", got)
	}
}

// 尾差并列：IL 相同，逐分给保单编号字典序最小者。
func TestRemainderTieBreak(t *testing.T) {
	// 损失 10，三张 IL 均为 10 => 每张 floor 3，尾差 1 分给编号最小者。
	var ps []*participant
	for _, no := range []string{"P3", "P1", "P2"} {
		p := mkPart(no, 0, 100, 1000, 1000, IndependentShare)
		p.il = independentLiability(p.policy, p.remaining, 10)
		ps = append(ps, p)
	}
	sortParticipants(ps)
	d := settle(ps, 10)
	got := paysOf(d)
	if got["P1"] != 4 || got["P2"] != 3 || got["P3"] != 3 {
		t.Fatalf("got %v want P1=4 P2=3 P3=3", got)
	}
}

// 超额型仅在非超额组未赔足时参与。
func TestExcessOnlyForShortfall(t *testing.T) {
	a := mkPart("A", 0, 1000, 5000, 5000, LimitShare) // 非超额可赔足
	x := mkPart("X", 0, 1000, 5000, 5000, Excess)
	for _, p := range []*participant{a, x} {
		p.il = independentLiability(p.policy, p.remaining, 1000)
	}
	d := settle([]*participant{a, x}, 1000)
	if d.stage2 != nil || paysOf(d)["X"] != 0 {
		t.Fatalf("excess must not join when primary covers all: %+v", d)
	}

	// 非超额只剩 600 能力，超额按 IL 比例接 400。
	a2 := mkPart("A", 0, 600, 5000, 5000, LimitShare)
	x1 := mkPart("X1", 0, 300, 5000, 5000, Excess)
	x2 := mkPart("X2", 0, 300, 5000, 5000, Excess)
	for _, p := range []*participant{a2, x1, x2} {
		p.il = independentLiability(p.policy, p.remaining, 1000)
	}
	d2 := settle([]*participant{a2, x1, x2}, 1000)
	got := paysOf(d2)
	if got["A"] != 600 || got["X1"] != 200 || got["X2"] != 200 {
		t.Fatalf("got %v want A=600 X1=200 X2=200", got)
	}
}

// IL 之和小于损失：两阶段合计封顶于各保单独立责任额之和。
func TestCappedByIndependentLiability(t *testing.T) {
	a := mkPart("A", 0, 300, 5000, 5000, LimitShare)
	x := mkPart("X", 0, 100, 5000, 5000, Excess)
	for _, p := range []*participant{a, x} {
		p.il = independentLiability(p.policy, p.remaining, 1000)
	}
	d := settle([]*participant{a, x}, 1000)
	got := paysOf(d)
	if got["A"] != 300 || got["X"] != 100 {
		t.Fatalf("got %v want A=300 X=100", got)
	}
}
