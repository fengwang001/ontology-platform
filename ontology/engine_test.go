package ontology

import (
	"errors"
	"testing"
)

func baseSpec() PolicySpec {
	return PolicySpec{
		InceptDay:          0,
		YearLen:            365,
		PerClaimDeductible: 100,
		AnnualDeductCap:    1000,
		InpatientRate:      80,
		OutpatientRate:     50,
		OOPCap:             500,
		ExcludedCodes:      map[string]bool{"EX": true},
	}
}

func claim(id string, day int64, lines ...Line) Claim {
	return Claim{ID: id, AccDay: day, Lines: lines}
}

func ln(code string, c Category, amt int64) Line { return Line{Code: code, Category: c, Amount: amt} }

func TestBasicSettlementAndConservation(t *testing.T) {
	e := NewEngine()
	if err := e.RegisterPolicy("p", baseSpec()); err != nil {
		t.Fatal(err)
	}
	// 住院 200：免赔100，余100*80%=80，自付=100免赔+20=120。
	res, err := e.Submit("p", claim("c1", 10, ln("A", CatInpatient, 200)))
	if err != nil {
		t.Fatal(err)
	}
	if res.InsurerPay != 80 || res.SelfPay != 120 || res.DeductApplied != 100 {
		t.Fatalf("got ins=%d self=%d ded=%d", res.InsurerPay, res.SelfPay, res.DeductApplied)
	}
	if res.InsurerPay+res.SelfPay+res.ExcludedAmount != 200 {
		t.Fatal("资金不守恒")
	}
	_, ded, oop, _ := e.YearSnapshot("p", 10)
	if ded != 100 || oop != 120 {
		t.Fatalf("ledger ded=%d oop=%d", ded, oop)
	}
}

func TestCoveredBaseEqualsAndBelowDeductible(t *testing.T) {
	s := baseSpec()
	r, _, _ := settle(claim("x", 0, ln("A", CatInpatient, 100)), s, 0, 0)
	if r.CoveredBase != 100 || r.DeductApplied != 100 || r.InsurerPay != 0 || r.SelfPay != 100 {
		t.Fatalf("equal: %+v", r)
	}
	r2, _, _ := settle(claim("x", 0, ln("A", CatInpatient, 60)), s, 0, 0)
	if r2.DeductApplied != 60 || r2.InsurerPay != 0 || r2.SelfPay != 60 {
		t.Fatalf("below: %+v", r2)
	}
}

func TestAnnualDeductCapLimitsThisClaim(t *testing.T) {
	s := baseSpec()
	// 已扣 950，上限 1000 -> 本次最多扣 50。
	r, newDed, _ := settle(claim("x", 0, ln("A", CatInpatient, 200)), s, 950, 0)
	if r.DeductApplied != 50 || newDed != 1000 {
		t.Fatalf("got ded=%d newDed=%d", r.DeductApplied, newDed)
	}
	// 已扣恰好 1000 -> 本次免赔 0。
	r2, newDed2, _ := settle(claim("y", 0, ln("A", CatInpatient, 200)), s, 1000, 0)
	if r2.DeductApplied != 0 || newDed2 != 1000 {
		t.Fatalf("capped ded=%d newDed=%d", r2.DeductApplied, newDed2)
	}
}

func TestDeductibleConsumesAcrossLines(t *testing.T) {
	s := baseSpec()
	// 免赔100：第一条60扣尽，第二条80中扣40，余40*80%=32。
	r, _, _ := settle(claim("x", 0,
		ln("A", CatInpatient, 60),
		ln("B", CatInpatient, 80)), s, 0, 0)
	if r.Lines[0].Deduct != 60 || r.Lines[1].Deduct != 40 {
		t.Fatalf("deduct split %d/%d", r.Lines[0].Deduct, r.Lines[1].Deduct)
	}
	if r.Lines[1].Insurer != 32 {
		t.Fatalf("line2 insurer=%d", r.Lines[1].Insurer)
	}
}

func TestCeilTail(t *testing.T) {
	s := baseSpec()
	// 住院 101 免赔后余1：ceil(0.8)=1；自付100。
	r, _, _ := settle(claim("x", 0, ln("A", CatInpatient, 101)), s, 0, 0)
	if r.Lines[0].Insurer != 1 || r.SelfPay != 100 {
		t.Fatalf("ceil: ins=%d self=%d", r.Lines[0].Insurer, r.SelfPay)
	}
	// 门诊 103 免赔后余3：ceil(1.5)=2。
	r2, _, _ := settle(claim("y", 0, ln("A", CatOutpatient, 103)), s, 0, 0)
	if r2.Lines[0].Insurer != 2 {
		t.Fatalf("ceil outpatient=%d", r2.Lines[0].Insurer)
	}
}

func TestOOPCapExactAndExceedAttribution(t *testing.T) {
	s := baseSpec() // OOPCap=500
	// 已自付480，room20；免赔100在第一条即截断：自付20，追加80；其后全额。
	r, newDed, newOOP := settle(claim("x", 0,
		ln("A", CatInpatient, 100),
		ln("B", CatInpatient, 100)), s, 0, 480)
	if newOOP != 500 {
		t.Fatalf("oop=%d", newOOP)
	}
	if r.Lines[0].SelfPay != 20 || r.Lines[0].CapShift != 80 || r.Lines[0].Insurer != 80 {
		t.Fatalf("cross line %+v", r.Lines[0])
	}
	if !r.Lines[1].FullAfter || r.Lines[1].Insurer != 100 || r.Lines[1].SelfPay != 0 {
		t.Fatalf("after line %+v", r.Lines[1])
	}
	// 截断发生在免赔段内：年度免赔账仍记名义免赔全额100，自付仅20。
	if newDed != 100 || r.DeductApplied != 100 {
		t.Fatalf("ded applied=%d newDed=%d", r.DeductApplied, newDed)
	}
	// 恰好等于封顶：不追加。
	r2, _, oop2 := settle(claim("y", 0, ln("A", CatInpatient, 200)), s, 0, 380)
	if oop2 != 500 || r2.Lines[0].CapShift != 0 {
		t.Fatalf("exact cap %+v oop=%d", r2.Lines[0], oop2)
	}
}

func TestAfterCapFullPayNoDeduct(t *testing.T) {
	s := baseSpec()
	r, ded, oop := settle(claim("x", 0,
		ln("A", CatInpatient, 500),
		ln("B", CatOutpatient, 300)), s, 0, 500)
	if r.DeductApplied != 0 || r.InsurerPay != 800 || r.SelfPay != 0 {
		t.Fatalf("full pay %+v", r)
	}
	for _, l := range r.Lines {
		if !l.FullAfter {
			t.Fatalf("line %s not full", l.Code)
		}
	}
	if ded != 0 || oop != 500 {
		t.Fatalf("ledger changed ded=%d oop=%d", ded, oop)
	}
}

func TestCapExactBoundaryWithinClaim(t *testing.T) {
	s := baseSpec() // OOPCap=500，每次免赔100
	// 已自付420，room80。本笔两条：第一条住院200 -> 免赔100+比例自付20=120，
	// 超出room，截断发生在第一条（非恰好边界），自付80，追加40。
	r, _, oop := settle(claim("x", 0,
		ln("A", CatInpatient, 200),
		ln("B", CatInpatient, 100)), s, 0, 420)
	if oop != 500 || r.Lines[0].SelfPay != 80 || r.Lines[0].CapShift != 40 {
		t.Fatalf("mid-cross %+v oop=%d", r.Lines[0], oop)
	}
	if !r.Lines[1].FullAfter || r.Lines[1].Insurer != 100 {
		t.Fatalf("after %+v", r.Lines[1])
	}

	// 恰好边界：构造第一条的封顶前自付恰等于 room，第二条全额。
	// 已自付 400，room100；第一条门诊200：免赔100+ceil(100*50%)=50赔 -> 自付150，超过。
	// 改为：免赔100后余0自付=100恰为room：用住院100（全被免赔吸收）。
	r2, _, oop2 := settle(claim("y", 0,
		ln("A", CatInpatient, 100),
		ln("B", CatInpatient, 100)), s, 0, 400)
	if oop2 != 500 {
		t.Fatalf("oop2=%d", oop2)
	}
	if r2.Lines[0].SelfPay != 100 || r2.Lines[0].CapShift != 0 {
		t.Fatalf("exact boundary line0 %+v", r2.Lines[0])
	}
	if !r2.Lines[1].FullAfter || r2.Lines[1].Insurer != 100 {
		t.Fatalf("exact boundary line1 %+v", r2.Lines[1])
	}
}

func TestExcludedCodeNotCounted(t *testing.T) {
	s := baseSpec()
	r, ded, oop := settle(claim("x", 0,
		ln("EX", CatInpatient, 999),
		ln("A", CatInpatient, 200)), s, 0, 0)
	if r.ExcludedAmount != 999 || r.CoveredBase != 200 {
		t.Fatalf("excluded=%+v", r)
	}
	if r.InsurerPay != 80 || r.SelfPay != 120 || ded != 100 || oop != 120 {
		t.Fatalf("counted excluded: %+v ded=%d oop=%d", r, ded, oop)
	}
}

func TestYearRightBoundary(t *testing.T) {
	s := baseSpec() // 年度 [0,365) [365,730)
	if yi := yearIndex(s, 364); yi != 0 {
		t.Fatalf("day364 year=%d", yi)
	}
	if yi := yearIndex(s, 365); yi != 1 {
		t.Fatalf("day365 year=%d", yi)
	}
	e := NewEngine()
	_ = e.RegisterPolicy("p", s)
	r1, _ := e.Submit("p", claim("a", 364, ln("A", CatInpatient, 200)))
	r2, _ := e.Submit("p", claim("b", 365, ln("A", CatInpatient, 200)))
	if r1.YearIndex != 0 || r2.YearIndex != 1 {
		t.Fatalf("years %d/%d", r1.YearIndex, r2.YearIndex)
	}
	if r2.DeductApplied != 100 {
		t.Fatalf("year2 deduct=%d", r2.DeductApplied)
	}
}

func TestCancelLastThenResubmit(t *testing.T) {
	e := NewEngine()
	_ = e.RegisterPolicy("p", baseSpec())
	_, _ = e.Submit("p", claim("a", 10, ln("A", CatInpatient, 200)))
	_, _ = e.Submit("p", claim("b", 11, ln("A", CatInpatient, 200)))
	if err := e.Cancel("p", "a"); !errors.Is(err, ErrNotLast) {
		t.Fatalf("cancel non-last err=%v", err)
	}
	if err := e.Cancel("p", "b"); err != nil {
		t.Fatal(err)
	}
	_, ded, oop, _ := e.YearSnapshot("p", 10)
	if ded != 100 || oop != 120 {
		t.Fatalf("after cancel ded=%d oop=%d", ded, oop)
	}
	if _, err := e.Submit("p", claim("b", 12, ln("A", CatInpatient, 200))); err != nil {
		t.Fatalf("resubmit %v", err)
	}
	if err := e.Cancel("p", "ghost"); !errors.Is(err, ErrClaimMissing) {
		t.Fatalf("missing %v", err)
	}
}

func TestNonLastCancelLeavesNoTrace(t *testing.T) {
	e := NewEngine()
	_ = e.RegisterPolicy("p", baseSpec())
	_, _ = e.Submit("p", claim("a", 10, ln("A", CatInpatient, 200)))
	_, _ = e.Submit("p", claim("b", 11, ln("A", CatInpatient, 200)))
	_, beforeD, beforeO, _ := e.YearSnapshot("p", 10)
	if err := e.Cancel("p", "a"); !errors.Is(err, ErrNotLast) {
		t.Fatal(err)
	}
	_, afterD, afterO, _ := e.YearSnapshot("p", 10)
	if afterD != beforeD || afterO != beforeO {
		t.Fatal("非末笔撤销改变了累计")
	}
	if _, err := e.Submit("p", claim("a", 12, ln("A", CatInpatient, 1))); !errors.Is(err, ErrClaimExists) {
		t.Fatalf("dup after failed cancel: %v", err)
	}
}

func TestRejectOrderPairs(t *testing.T) {
	e := NewEngine()
	good := baseSpec()
	_ = e.RegisterPolicy("p", good)

	// 非法优先：重复登记。
	if err := e.RegisterPolicy("p", good); !errors.Is(err, ErrInvalid) {
		t.Fatalf("dup register: %v", err)
	}
	// 空号+空明细+未知保单：非法优先于保单不存在。
	if _, err := e.Submit("nope", claim("", 10)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid vs missing policy: %v", err)
	}
	// 非法比例/年度长度/金额。
	badSpec := good
	badSpec.InpatientRate = 101
	if err := e.RegisterPolicy("q", badSpec); !errors.Is(err, ErrInvalid) {
		t.Fatalf("rate range: %v", err)
	}
	badSpec = good
	badSpec.YearLen = 0
	if err := e.RegisterPolicy("q", badSpec); !errors.Is(err, ErrInvalid) {
		t.Fatalf("yearlen: %v", err)
	}
	if _, err := e.Submit("p", claim("z", 10, ln("A", CatUnknown, 10))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown category: %v", err)
	}
	if _, err := e.Submit("p", claim("z", 10, ln("A", CatInpatient, 0))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nonpositive: %v", err)
	}

	_, _ = e.Submit("p", claim("dup", 10, ln("A", CatInpatient, 10)))
	// 重复号 且 事故日未承保：已存在优先。
	if _, err := e.Submit("p", claim("dup", -5, ln("A", CatInpatient, 10))); !errors.Is(err, ErrClaimExists) {
		t.Fatalf("exists vs notcovered: %v", err)
	}
	// 新号 + 事故日未承保。
	if _, err := e.Submit("p", claim("new", -1, ln("A", CatInpatient, 10))); !errors.Is(err, ErrNotCovered) {
		t.Fatalf("notcovered: %v", err)
	}
	// 撤销空号：非法优先于保单不存在。
	if err := e.Cancel("nope", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cancel empty: %v", err)
	}
	// 保单不存在优先于理赔不存在。
	if err := e.Cancel("nope", "x"); !errors.Is(err, ErrPolicyMissing) {
		t.Fatalf("cancel missing policy: %v", err)
	}
	// 理赔不存在优先于非末笔。
	if err := e.Cancel("p", "ghost"); !errors.Is(err, ErrClaimMissing) {
		t.Fatalf("claim missing: %v", err)
	}
	// 被拒绝操作不留痕：再次提交同号 new（之前被事故日未承保拒绝）应成功。
	if _, err := e.Submit("p", claim("new", 20, ln("A", CatInpatient, 10))); err != nil {
		t.Fatalf("rejected submit left trace: %v", err)
	}
}
