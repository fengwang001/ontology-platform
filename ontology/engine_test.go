package ontology

import "testing"

func ePol(insured, id string, clause Clause, ded, per, annual int64) Policy {
	return Policy{
		Insured: insured, PolicyID: id, Deductible: ded, PerLossLimit: per,
		AnnualLimit: annual, CoverFrom: 0, CoverTo: 100, Clause: clause,
	}
}

func codeOf(err error) ErrorCode {
	t := err.(*EngineError)
	return t.Code
}

// 撤销末笔恢复累计；非末笔撤销不留痕。
func TestCancelLastRestores(t *testing.T) {
	e := NewEngine()
	must(t, e.RegisterPolicy(ePol("I", "P1", ClauseLimitProportional, 0, 1000, 1000)))
	must(t, e.RegisterPolicy(ePol("I", "P2", ClauseLimitProportional, 0, 1000, 1000)))
	r1, err := e.AcceptLoss(Loss{LossID: "L1", Insured: "I", Day: 1, Amount: 400})
	mustOK(t, r1, err)
	r2, err := e.AcceptLoss(Loss{LossID: "L2", Insured: "I", Day: 1, Amount: 200})
	mustOK(t, r2, err)
	b1, _, _ := e.Balance("I", "P1")
	if b1 != 1000-200-100 {
		t.Fatalf("before cancel P1 = %d", b1)
	}

	// 撤销非末笔 L1 -> 非末笔，账不变、记录仍在。
	if err := e.CancelLoss("I", "L1"); codeOf(err) != ErrNotLast {
		t.Fatalf("want 非末笔, got %v", err)
	}
	if b, _, _ := e.Balance("I", "P1"); b != 700 {
		t.Fatalf("non-last cancel changed balance: %d", b)
	}
	if _, ok := e.LossPaid("L1"); !ok {
		t.Fatal("L1 should still exist")
	}

	// 撤销末笔 L2 -> 恢复受理前。
	if err := e.CancelLoss("I", "L2"); err != nil {
		t.Fatalf("cancel last: %v", err)
	}
	if b, _, _ := e.Balance("I", "P1"); b != 800 {
		t.Fatalf("after cancel P1 = %d, want 800", b)
	}
	if b, _, _ := e.Balance("I", "P2"); b != 800 {
		t.Fatalf("after cancel P2 = %d, want 800", b)
	}
	if _, ok := e.LossPaid("L2"); ok {
		t.Fatal("L2 should be removed")
	}
	// 撤销不存在的损失号。
	if err := e.CancelLoss("I", "LX"); codeOf(err) != ErrLossMissing {
		t.Fatalf("want 损失不存在, got %v", err)
	}
	// 撤销后同号损失可重新受理（重放等价性）。
	_, err = e.AcceptLoss(Loss{LossID: "L2", Insured: "I", Day: 1, Amount: 200})
	must(t, err)
}

// 无任何参与保单的损失以「无可赔保单」受理，但占用受理次序、可撤销。
func TestNoPayerAcceptedAndCancellable(t *testing.T) {
	e := NewEngine()
	must(t, e.RegisterPolicy(ePol("I", "P1", ClauseLimitProportional, 0, 1000, 1000)))
	r, err := e.AcceptLoss(Loss{LossID: "L1", Insured: "I", Day: 500, Amount: 100})
	mustOK(t, r, err)
	if !r.NoPayer || r.Paid != 0 {
		t.Fatalf("want no payer: %+v", r)
	}
	if b, _, _ := e.Balance("I", "P1"); b != 1000 {
		t.Fatalf("balance changed: %d", b)
	}
	if err := e.CancelLoss("I", "L1"); err != nil {
		t.Fatalf("cancel no-payer: %v", err)
	}
}

// 拒绝次序：参数非法 > 被保人不存在 > 保单重复 > 损失已存在 > 损失不存在 > 非末笔。
func TestRejectOrder(t *testing.T) {
	e := NewEngine()
	must(t, e.RegisterPolicy(ePol("I", "P1", ClauseLimitProportional, 0, 1000, 1000)))
	_, err := e.AcceptLoss(Loss{LossID: "L1", Insured: "I", Day: 1, Amount: 400})
	must(t, err)
	_, err = e.AcceptLoss(Loss{LossID: "L2", Insured: "I", Day: 1, Amount: 400})
	must(t, err)

	type tc struct {
		name string
		op   func() error
		want ErrorCode
	}
	dupPol := ePol("I", "P1", ClauseLimitProportional, 0, 1000, 1000)
	cases := []tc{
		// 参数非法优先于一切（同时触发保单重复等）。
		{"invalid>dup", func() error {
			bad := dupPol
			bad.PerLossLimit = 0
			return e.RegisterPolicy(bad)
		}, ErrInvalid},
		{"invalid>insuredMissing", func() error {
			return e.RegisterPolicy(ePol("NOBODY", "Q", Clause(9), 0, 1000, 1000))
		}, ErrInvalid},
		{"invalid>lossExists", func() error {
			_, er := e.AcceptLoss(Loss{LossID: "L1", Insured: "I", Day: 1, Amount: 0})
			return er
		}, ErrInvalid},
		{"invalid>lossMissing", func() error {
			return e.CancelLoss("I", "")
		}, ErrInvalid},
		// 被保人不存在优先于损失已存在/损失不存在/非末笔。
		{"insuredMissing>lossExists", func() error {
			_, er := e.AcceptLoss(Loss{LossID: "L1", Insured: "GHOST", Day: 1, Amount: 1})
			return er
		}, ErrInsuredMissing},
		{"insuredMissing>lossMissing", func() error {
			return e.CancelLoss("GHOST", "ZZ")
		}, ErrInsuredMissing},
		// 保单重复（金额等其余参数合法）。
		{"policyDuplicate", func() error { return e.RegisterPolicy(dupPol) }, ErrPolicyDuplicate},
		// 损失已存在优先于非末笔（L1 已存在且非末笔）。
		{"lossExists>notLast", func() error {
			_, er := e.AcceptLoss(Loss{LossID: "L1", Insured: "I", Day: 1, Amount: 1})
			return er
		}, ErrLossExists},
		// 损失不存在优先于非末笔。
		{"lossMissing>notLast", func() error { return e.CancelLoss("I", "L9") }, ErrLossMissing},
		// 非末笔。
		{"notLast", func() error { return e.CancelLoss("I", "L1") }, ErrNotLast},
	}
	for _, c := range cases {
		if got := codeOf(c.op()); got != c.want {
			t.Fatalf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	badRange := ePol("I", "PR", ClauseLimitProportional, 0, 1000, 1000)
	badRange.CoverFrom, badRange.CoverTo = 10, 10
	if got := codeOf(e.RegisterPolicy(badRange)); got != ErrInvalid {
		t.Fatalf("badRange: got %v", got)
	}
	badClause := ePol("I", "PC", Clause(42), 0, 1000, 1000)
	if got := codeOf(e.RegisterPolicy(badClause)); got != ErrInvalid {
		t.Fatalf("badClause: got %v", got)
	}
}

// 同号损失在不同被保人之间也不允许重复（全引擎唯一）。
func TestLossIDGlobalUnique(t *testing.T) {
	e := NewEngine()
	must(t, e.RegisterPolicy(ePol("I", "P1", ClauseLimitProportional, 0, 1000, 1000)))
	must(t, e.RegisterPolicy(ePol("J", "P1", ClauseLimitProportional, 0, 1000, 1000)))
	_, err := e.AcceptLoss(Loss{LossID: "L1", Insured: "I", Day: 1, Amount: 10})
	must(t, err)
	if _, err := e.AcceptLoss(Loss{LossID: "L1", Insured: "J", Day: 1, Amount: 10}); codeOf(err) != ErrLossExists {
		t.Fatalf("global uniqueness: %v", err)
	}
}
