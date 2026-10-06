package apportion

import (
	"bytes"
	"errors"
	"testing"
)

func reg(t *testing.T, e *Engine, insured, no string, ded, per, annual, start, end int64, c Clause) {
	t.Helper()
	if err := e.Register(Policy{
		Insured: insured, PolicyNo: no, Deductible: ded, PerLoss: per,
		AnnualLimit: annual, StartDay: start, EndDay: end, Clause: c,
	}); err != nil {
		t.Fatalf("register %s: %v", no, err)
	}
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var re *Error
	if !errors.As(err, &re) || re.Code != code {
		t.Fatalf("err=%v want code %q", err, code)
	}
}

func payMap(o *Outcome) map[string]int64 {
	m := map[string]int64{}
	for _, p := range o.Payouts {
		m[p.PolicyNo] = p.Amount
	}
	return m
}

// 损失日恰等于承保区间右端不参与；等于左端参与。
func TestDayBounds(t *testing.T) {
	e := NewEngine(nil)
	reg(t, e, "I", "A", 0, 1000, 5000, 0, 10, LimitShare)

	out, err := e.Accept(Loss{LossNo: "Lr", Insured: "I", Day: 10, Amount: 500})
	if err != nil {
		t.Fatal(err)
	}
	if out.Conclusion != ConclusionNoPolicy {
		t.Fatalf("day==end must not be covered, got %s", out.Conclusion)
	}
	out, err = e.Accept(Loss{LossNo: "Ll", Insured: "I", Day: 0, Amount: 500})
	if err != nil {
		t.Fatal(err)
	}
	if payMap(out)["A"] != 500 {
		t.Fatalf("day==start must be covered: %v", payMap(out))
	}
}

// 年度累计恰好耗尽的保单不入分母，视同不存在。
func TestExhaustedPolicyExcluded(t *testing.T) {
	e := NewEngine(nil)
	// A、B 每次限额均为 500；A 年度累计仅 500。L1=1000：权重 500:500 各 500，
	// A 恰好耗尽。
	reg(t, e, "I", "A", 0, 500, 500, 0, 100, LimitShare)
	reg(t, e, "I", "B", 0, 500, 50000, 0, 100, LimitShare)
	out, err := e.Accept(Loss{LossNo: "L1", Insured: "I", Day: 1, Amount: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if payMap(out)["A"] != 500 || payMap(out)["B"] != 500 || out.Remaining["A"] != 0 {
		t.Fatalf("L1: pays=%v rem=%v", payMap(out), out.Remaining)
	}
	// L2：A 恰好耗尽视同不存在，200 全部由 B 赔。
	out, err = e.Accept(Loss{LossNo: "L2", Insured: "I", Day: 1, Amount: 200})
	if err != nil {
		t.Fatal(err)
	}
	if payMap(out)["A"] != 0 || payMap(out)["B"] != 200 {
		t.Fatalf("exhausted A excluded, got %v", payMap(out))
	}
}

// 两种封顶：合计恰等于损失金额；合计恰等于独立责任额之和。
func TestTwoCaps(t *testing.T) {
	e := NewEngine(nil)
	reg(t, e, "I", "A", 0, 1000, 9000, 0, 100, IndependentShare)
	reg(t, e, "I", "B", 0, 1000, 9000, 0, 100, IndependentShare)
	reg(t, e, "I", "X", 0, 1000, 9000, 0, 100, Excess)
	out, err := e.Accept(Loss{LossNo: "L1", Insured: "I", Day: 1, Amount: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if total := sumPays(out); total != 1000 {
		t.Fatalf("cap by loss: total=%d", total)
	}

	// A IL=300；X 免赔100限额300 => IL=300；IL 之和 600 < 损失 1000。
	e2 := NewEngine(nil)
	reg(t, e2, "J", "A", 0, 300, 9000, 0, 100, LimitShare)
	reg(t, e2, "J", "X", 100, 300, 9000, 0, 100, Excess)
	out, err = e2.Accept(Loss{LossNo: "L1", Insured: "J", Day: 1, Amount: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if total := sumPays(out); total != 600 {
		t.Fatalf("cap by IL sum: total=%d want 600", total)
	}
	if m := payMap(out); m["A"] != 300 || m["X"] != 300 {
		t.Fatalf("cap payout: %v", m)
	}
}

func sumPays(o *Outcome) int64 {
	var s int64
	for _, p := range o.Payouts {
		s += p.Amount
	}
	return s
}

// 撤销末笔恢复累计；非末笔撤销不留痕、不释放损失号。
func TestUndoLastAndNonLast(t *testing.T) {
	e := NewEngine(nil)
	reg(t, e, "I", "A", 0, 1000, 1000, 0, 100, LimitShare)
	o1, err := e.Accept(Loss{LossNo: "L1", Insured: "I", Day: 1, Amount: 400})
	if err != nil {
		t.Fatal(err)
	}
	if o1.Remaining["A"] != 600 {
		t.Fatalf("after L1 rem=%d", o1.Remaining["A"])
	}
	if _, err := e.Accept(Loss{LossNo: "L2", Insured: "I", Day: 1, Amount: 100}); err != nil {
		t.Fatal(err)
	}

	if _, err := e.Undo("L1", "I"); err == nil {
		t.Fatal("non-last undo must fail")
	} else {
		wantCode(t, err, ErrNotLast)
	}
	if rem := e.Remaining("I")["A"]; rem != 500 {
		t.Fatalf("non-last undo changed ledger, rem=%d", rem)
	}
	_, err = e.Accept(Loss{LossNo: "L1", Insured: "I", Day: 1, Amount: 1})
	wantCode(t, err, ErrLossDup)

	if _, err := e.Undo("L2", "I"); err != nil {
		t.Fatal(err)
	}
	if rem := e.Remaining("I")["A"]; rem != 600 {
		t.Fatalf("after undo L2 rem=%d", rem)
	}
	if _, err := e.Undo("L1", "I"); err != nil {
		t.Fatal(err)
	}
	if rem := e.Remaining("I")["A"]; rem != 1000 {
		t.Fatalf("after undo L1 rem=%d", rem)
	}

	// 重放得到完全相同的应赔与余额。
	o3, err := e.Accept(Loss{LossNo: "L1", Insured: "I", Day: 1, Amount: 400})
	if err != nil {
		t.Fatal(err)
	}
	if payMap(o3)["A"] != 400 || o3.Remaining["A"] != 600 {
		t.Fatalf("re-accept not reproducible: %v rem=%d", payMap(o3), o3.Remaining["A"])
	}

	_, err = e.Undo("NOPE", "I")
	wantCode(t, err, ErrLossMissing)
}

// 无参与保单：以「无可赔保单」受理，账不变，损失号在册且可撤销。
func TestNoPolicyAcceptance(t *testing.T) {
	var buf bytes.Buffer
	e := NewEngine(&buf)
	reg(t, e, "I", "A", 0, 100, 500, 0, 10, LimitShare)
	out, err := e.Accept(Loss{LossNo: "L1", Insured: "I", Day: 50, Amount: 999})
	if err != nil {
		t.Fatal(err)
	}
	if out.Conclusion != ConclusionNoPolicy || len(out.Payouts) != 0 {
		t.Fatalf("want no-policy, got %s %v", out.Conclusion, out.Payouts)
	}
	if e.Remaining("I")["A"] != 500 {
		t.Fatal("ledger must not change")
	}
	_, err = e.Accept(Loss{LossNo: "L1", Insured: "I", Day: 50, Amount: 1})
	wantCode(t, err, ErrLossDup)
	if _, err := e.Undo("L1", "I"); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Fatal("decision log expected")
	}
}

// 拒绝次序：参数非法 > 被保人不存在 > 保单重复 > 损失已存在 > 损失不存在 > 非末笔。
func TestRejectionOrder(t *testing.T) {
	e := NewEngine(nil)
	reg(t, e, "I", "A", 0, 100, 500, 0, 10, LimitShare)
	if _, err := e.Accept(Loss{LossNo: "L1", Insured: "I", Day: 1, Amount: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Accept(Loss{LossNo: "L2", Insured: "I", Day: 1, Amount: 10}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		fn   func() error
		code string
	}{
		{"登记:参数非法(优先于保单重复)", func() error {
			return e.Register(Policy{Insured: "", PolicyNo: "A", Clause: LimitShare})
		}, ErrInvalid},
		{"登记:保单重复", func() error {
			return e.Register(Policy{Insured: "K", PolicyNo: "A", PerLoss: 1, AnnualLimit: 1, StartDay: 0, EndDay: 1, Clause: LimitShare})
		}, ErrPolicyDup},
		{"受理:参数非法(优先于被保人缺失)", func() error {
			_, err := e.Accept(Loss{LossNo: "", Insured: "ZZ", Amount: 0})
			return err
		}, ErrInvalid},
		{"受理:被保人不存在", func() error {
			_, err := e.Accept(Loss{LossNo: "Q", Insured: "ZZ", Amount: 1})
			return err
		}, ErrInsuredMissing},
		{"受理:参数非法优先于损失已存在", func() error {
			_, err := e.Accept(Loss{LossNo: "L1", Insured: "I", Amount: 0})
			return err
		}, ErrInvalid},
		{"受理:损失已存在", func() error {
			_, err := e.Accept(Loss{LossNo: "L1", Insured: "I", Amount: 1})
			return err
		}, ErrLossDup},
		{"撤销:参数非法", func() error {
			_, err := e.Undo("", "")
			return err
		}, ErrInvalid},
		{"撤销:被保人不存在(优先于损失不存在)", func() error {
			_, err := e.Undo("L1", "ZZ")
			return err
		}, ErrInsuredMissing},
		{"撤销:损失不存在(优先于非末笔)", func() error {
			_, err := e.Undo("GHOST", "I")
			return err
		}, ErrLossMissing},
		{"撤销:非末笔", func() error {
			_, err := e.Undo("L1", "I")
			return err
		}, ErrNotLast},
		{"未知条款类型", func() error {
			return e.Register(Policy{Insured: "I", PolicyNo: "B", PerLoss: 1, AnnualLimit: 1, StartDay: 0, EndDay: 1, Clause: Clause(9)})
		}, ErrInvalid},
		{"区间左端不小于右端", func() error {
			return e.Register(Policy{Insured: "I", PolicyNo: "C", PerLoss: 1, AnnualLimit: 1, StartDay: 3, EndDay: 3})
		}, ErrInvalid},
		{"负免赔/非正限额", func() error {
			return e.Register(Policy{Insured: "I", PolicyNo: "D", Deductible: -1, PerLoss: 1, AnnualLimit: 1, StartDay: 0, EndDay: 1})
		}, ErrInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantCode(t, tc.fn(), tc.code)
		})
	}

	// 被拒绝操作不改账：全部拒绝后撤销末笔仍应正常恢复。
	if _, err := e.Undo("L2", "I"); err != nil {
		t.Fatal(err)
	}
	if rem := e.Remaining("I")["A"]; rem != 400 {
		t.Fatalf("rejected ops must not change ledger, rem=%d", rem)
	}
}
